package session

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmConfig "github.com/aws/session-manager-plugin/src/config"
	"github.com/aws/session-manager-plugin/src/datachannel"
	"github.com/aws/session-manager-plugin/src/log"
	"github.com/aws/session-manager-plugin/src/message"
	"github.com/aws/session-manager-plugin/src/retry"
	awsSession "github.com/aws/session-manager-plugin/src/sessionmanagerplugin/session"
	"github.com/borderzero/border0-cli/internal/ssh/config"
	"github.com/manifoldco/promptui"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

const (
	StdinBufferLimit    = 1024
	ResizeSleepInterval = 500 * time.Millisecond
)

type ssmSessionHandler struct {
	logger    *zap.Logger
	config    *config.ProxyConfig
	ssmClient *ssm.Client
}

type ssmSession struct {
	awsSession.Session
	retryParams        retry.RepeatableExponentialRetryer
	logger             *zap.Logger
	config             *config.ProxyConfig
	sessionKey         *string
	downstreamSshConn  *ssh.ServerConn
	downstreamSshChans <-chan ssh.NewChannel
	downstreamSshReqs  <-chan *ssh.Request
	sshChannel         ssh.Channel
	sshHeight          int
	sshWidth           int
	awsSSMTarget       string
	ssmClient          *ssm.Client
}

func NewSsmSession(logger *zap.Logger, config *config.ProxyConfig) (*ssmSessionHandler, error) {
	ssmClient := ssm.NewFromConfig(config.AwsConfig)

	if config.ECSSSMProxy != nil {
		ecsSvc := ecs.NewFromConfig(config.AwsConfig)

		input := &ecs.ListTasksInput{
			Cluster:       &config.ECSSSMProxy.Cluster,
			DesiredStatus: types.DesiredStatusRunning,
		}

		output, err := ecsSvc.ListTasks(context.TODO(), input)
		if err != nil {
			return nil, fmt.Errorf("unable to gather ECS cluster tasks, %v", err)
		}

		if len(output.TaskArns) == 0 {
			logger.Sugar().Infof("sshauthproxy: no running tasks found in ECS cluster %s", config.ECSSSMProxy.Cluster)
		}
	}

	return &ssmSessionHandler{
		ssmClient: ssmClient,
		config:    config,
		logger:    logger,
	}, nil
}

func (s *ssmSessionHandler) Proxy(conn net.Conn) {
	defer conn.Close()

	sshConn, chans, reqs, sessionKey, _, err := newSSHServerConn(conn, s.config)
	if err != nil {
		s.logger.Sugar().Errorf("failed to accept ssh connection: %s", err)
		return
	}

	session := &ssmSession{
		config:             s.config,
		downstreamSshConn:  sshConn,
		downstreamSshChans: chans,
		downstreamSshReqs:  reqs,
		logger:             s.logger,
		sshWidth:           80,
		sshHeight:          24,
		awsSSMTarget:       s.config.AwsSSMTarget,
		ssmClient:          s.ssmClient,
	}

	if sessionKey != nil {
		session.sessionKey = sessionKey
		session.logger = session.logger.With(zap.String("session_key", *sessionKey))
	}

	go ssh.DiscardRequests(reqs)

	if err := session.handleChannels(); err != nil {
		s.logger.Error("failed to handle channels", zap.Error(err))
		return
	}
}

func (s *ssmSession) handleChannels() error {
	defer s.downstreamSshConn.Close()

	for newChannel := range s.downstreamSshChans {
		if newChannel == nil {
			return fmt.Errorf("proxy channel closed")
		}

		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			return fmt.Errorf("failed to accept channel: %s", err)
		}

		go func(in <-chan *ssh.Request) {
			for req := range in {
				switch {
				case req == nil:
					continue
				case req.Type == "pty-req":
					termLen := req.Payload[3]
					w, h := parseDims(req.Payload[termLen+4:])
					s.sshWidth = int(w)
					s.sshHeight = int(h)

					if req.WantReply {
						req.Reply(true, nil)
					}
				case req.Type == "window-change":
					w, h := parseDims(req.Payload)
					s.sshWidth = int(w)
					s.sshHeight = int(h)

					if err := s.handleWindowChange(int(w), int(h)); err != nil {
						s.logger.Error("failed to handle window change", zap.Error(err))
						req.Reply(false, nil)
						return
					}

					if req.WantReply {
						req.Reply(true, nil)
					}
				case req.Type == "shell":
					go s.handleSSMShell(channel, req)
				default:
					req.Reply(false, nil)
				}
			}
		}(requests)
	}

	return nil
}

// func (s *ssmSession) record(reader io.Reader) (*Recording, error) {
// 	pr, pw := io.Pipe()
// 	reader = io.TeeReader(reader, pw)

// 	r := NewRecording(s.logger, pr, *s.sessionKey, s.config.Border0API, s.sshWidth, s.sshHeight)

// 	if err := r.Record(); err != nil {
// 		return nil, err
// 	}

// 	return r, nil
// }

func (s *ssmSession) pickAwsEcsTargetForAwsSsm(channel ssh.Channel) error {
	ecsSvc := ecs.NewFromConfig(s.config.AwsConfig)
	var selectedCluster string
	if s.config.ECSSSMProxy.Cluster == "" {
		var clusters []string
		input := &ecs.ListClustersInput{}
		for {
			output, err := ecsSvc.ListClusters(context.TODO(), input)
			if err != nil {
				return fmt.Errorf("unable to list clusters, %v", err)
			}

			clusters = append(clusters, output.ClusterArns...)
			if output.NextToken == nil {
				break
			}
			input.NextToken = output.NextToken
		}

		if len(clusters) == 0 {
			return fmt.Errorf("no clusters found")
		}

		output, err := ecsSvc.DescribeClusters(context.TODO(), &ecs.DescribeClustersInput{Clusters: clusters})
		if err != nil {
			return fmt.Errorf("unable to describe clusters, %v", err)
		}

		var clusterNames []string

		for _, cluster := range output.Clusters {
			clusterNames = append(clusterNames, *cluster.ClusterName)
		}

		prompt := promptui.Select{
			Label:             "Choose a cluster",
			Items:             clusterNames,
			Stdout:            channel,
			Stdin:             channel,
			StartInSearchMode: true,
			Searcher: func(input string, index int) bool {
				return strings.Contains(strings.ToLower(clusterNames[index]), strings.ToLower(input))
			},
		}

		_, selectedCluster, err = prompt.Run()
		if err != nil {
			return fmt.Errorf("unable to select cluster, %v", err)
		}

	} else {
		selectedCluster = s.config.ECSSSMProxy.Cluster
	}

	var tasksArns []string
	input := &ecs.ListTasksInput{
		Cluster:       &selectedCluster,
		DesiredStatus: types.DesiredStatusRunning,
	}

	for {
		output, err := ecsSvc.ListTasks(context.TODO(), input)
		if err != nil {
			return fmt.Errorf("unable to list tasks, %v", err)
		}

		tasksArns = append(tasksArns, output.TaskArns...)
		if output.NextToken == nil {
			break
		}
		input.NextToken = output.NextToken
	}

	if len(tasksArns) == 0 {
		return fmt.Errorf("no tasks found")
	}

	var tasks []string
	tasksIDS := make(map[string]string)
	containers := make(map[string]map[string]string)

	tasksIput := &ecs.DescribeTasksInput{
		Cluster: &selectedCluster,
		Tasks:   tasksArns,
	}

	output, err := ecsSvc.DescribeTasks(context.TODO(), tasksIput)
	if err != nil {
		return fmt.Errorf("unable to describe tasks, %v", err)
	}

	for _, task := range output.Tasks {
		taskDefinitionArn := *task.TaskDefinitionArn
		taskParts := strings.Split(taskDefinitionArn, "/")
		if len(taskParts) != 2 {
			return fmt.Errorf("invalid task definition arn: %s", taskDefinitionArn)
		}

		taskName := taskParts[1]
		if len(s.config.ECSSSMProxy.Tasks) > 0 {
			var found bool
			for _, c := range s.config.ECSSSMProxy.Tasks {
				if strings.HasPrefix(strings.ToLower(taskName), strings.ToLower(c)) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		if len(s.config.ECSSSMProxy.Services) > 0 {
			service := strings.TrimPrefix(*task.Group, "service:")
			var found bool
			for _, c := range s.config.ECSSSMProxy.Services {
				if strings.EqualFold(c, service) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		taskArnParts := strings.Split(*task.TaskArn, "/")
		if len(taskArnParts) != 3 {
			return fmt.Errorf("invalid task arn: %s", *task.TaskArn)
		}

		taskName = fmt.Sprintf("%s (%s)", taskName, taskArnParts[2])
		tasks = append(tasks, taskName)

		tasksIDS[taskName] = taskArnParts[2]
		containers[taskName] = make(map[string]string)
		for _, container := range task.Containers {
			if container.RuntimeId == nil || container.Name == nil {
				continue
			}

			if len(s.config.ECSSSMProxy.Containers) > 0 {
				var found bool
				for _, c := range s.config.ECSSSMProxy.Containers {
					if strings.EqualFold(c, *container.Name) {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}
			containers[taskName][*container.Name] = *container.RuntimeId
		}
	}

	if len(tasks) == 0 {
		return fmt.Errorf("no tasks found")
	}

	prompt := promptui.Select{
		Label:             "Choose a task",
		Items:             tasks,
		Stdout:            channel,
		Stdin:             channel,
		StartInSearchMode: true,
		Searcher: func(input string, index int) bool {
			return strings.Contains(strings.ToLower(tasks[index]), strings.ToLower(input))
		},
	}

	_, selectedTask, err := prompt.Run()
	if err != nil {
		return fmt.Errorf("unable to select tasks, %v", err)
	}

	if len(containers[selectedTask]) == 0 {
		return fmt.Errorf("no containers found")
	}

	var containerNames []string
	for container := range containers[selectedTask] {
		containerNames = append(containerNames, container)
	}

	prompt = promptui.Select{
		Label:             "Choose a container",
		Items:             containerNames,
		Stdout:            channel,
		Stdin:             channel,
		StartInSearchMode: true,
		Searcher: func(input string, index int) bool {
			return strings.Contains(strings.ToLower(containerNames[index]), strings.ToLower(input))
		},
	}

	_, selectedContainer, err := prompt.Run()
	if err != nil {
		return fmt.Errorf("unable to select container, %v", err)
	}

	s.awsSSMTarget = fmt.Sprintf("ecs:%s_%s_%s", selectedCluster, tasksIDS[selectedTask], containers[selectedTask][selectedContainer])

	return nil
}

type ssmDataChannel struct {
	datachannel.DataChannel
}

func (s ssmDataChannel) HandleChannelClosedMessage(log log.T, stopHandler datachannel.Stop, sessionId string, outputMessage message.ClientMessage) {
	stopHandler()
}

func (dataChannel *ssmDataChannel) OutputMessageHandler(log log.T, stopHandler datachannel.Stop, sessionID string, rawMessage []byte) error {
	outputMessage := &message.ClientMessage{}
	err := outputMessage.DeserializeClientMessage(log, rawMessage)
	if err != nil {
		log.Errorf("Cannot deserialize raw message: %s, err: %v.", string(rawMessage), err)
		return err
	}
	if err = outputMessage.Validate(); err != nil {
		log.Errorf("Invalid outputMessage: %v, err: %v.", *outputMessage, err)
		return err
	}

	switch outputMessage.MessageType {
	case message.OutputStreamMessage:
		return dataChannel.HandleOutputMessage(log, *outputMessage, rawMessage)
	case message.AcknowledgeMessage:
		return dataChannel.HandleAcknowledgeMessage(log, *outputMessage)
	case message.ChannelClosedMessage:
		dataChannel.HandleChannelClosedMessage(log, stopHandler, sessionID, *outputMessage)
	case message.StartPublicationMessage, message.PausePublicationMessage:
		return nil
	default:
		log.Warn("Invalid message type received: %s", outputMessage.MessageType)
	}

	return nil
}

type PipeWriteChannel struct {
	ssh.Channel
	writer    io.Writer
	reader    io.ReadCloser
	logWriter io.Writer
}

func NewPipeWriteChannel(channel ssh.Channel) *PipeWriteChannel {
	pr, pw := io.Pipe()

	return &PipeWriteChannel{
		Channel:   channel,
		writer:    io.MultiWriter(channel, pw),
		reader:    pr,
		logWriter: pw,
	}
}

func (pwc *PipeWriteChannel) Write(data []byte) (int, error) {
	return pwc.writer.Write(data)
}

func (s *ssmSession) handleSSMShell(channel ssh.Channel, req *ssh.Request) {
	defer channel.Close()

	if err := req.Reply(true, nil); err != nil {
		s.logger.Error("failed to reply to request", zap.Error(err))
		return
	}

	if s.config.IsRecordingEnabled() {
		pwc := NewPipeWriteChannel(channel)
		channel = pwc

		r := NewRecording(s.logger, pwc.reader, *s.sessionKey, s.config.Border0API, s.sshWidth, s.sshHeight)
		if err := r.Record(); err != nil {
			s.logger.Error("failed to record session", zap.Error(err))
			return
		}

		defer r.Stop()
	}

	if s.config.ECSSSMProxy != nil {
		if err := s.pickAwsEcsTargetForAwsSsm(channel); err != nil {
			s.logger.Sugar().Errorf("failed to pick ECS target: %s", err)
			req.Reply(false, nil)
			s.downstreamSshConn.Close()
			return
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sessionOutput, err := s.ssmClient.StartSession(ctx, &ssm.StartSessionInput{
		Target: &s.awsSSMTarget,
	})
	if err != nil {
		s.logger.Sugar().Errorf("failed to start ssm session: %s\n", err)
		return
	}

	datachannel := ssmDataChannel{
		DataChannel: datachannel.DataChannel{},
	}

	s.SessionId = *sessionOutput.SessionId
	s.StreamUrl = *sessionOutput.StreamUrl
	s.TokenValue = *sessionOutput.TokenValue
	s.DataChannel = &datachannel
	sessionLogger := log.Logger(false, "border0")

	if err = s.OpenDataChannel(sessionLogger); err != nil {
		s.logger.Sugar().Errorf("failed to execute ssm session: %s\n", err)
		return
	}

	defer s.TerminateSession(sessionLogger)

	s.SessionType = "border0"
	s.sshChannel = channel

	s.Initialize(sessionLogger)
	if err := s.handleWindowChange(s.sshWidth, s.sshHeight); err != nil {
		s.logger.Error("failed to set window size", zap.Error(err))
		return
	}

	if s.SetSessionHandlers(sessionLogger); err != nil {
		s.logger.Sugar().Errorf("failed to execute ssm session: %s\n", err)
	}
}

func (s *ssmSession) Initialize(log log.T) {
	s.DataChannel.RegisterOutputStreamHandler(s.ProcessStreamMessagePayload, true)
	s.DataChannel.GetWsChannel().SetOnMessage(
		func(input []byte) {
			s.DataChannel.OutputMessageHandler(log, s.Stop, s.SessionId, input)
		})

}

func (s *ssmSession) Stop() {
	s.sshChannel.Close()
}

// StartSession takes input and write it to data channel
func (s *ssmSession) SetSessionHandlers(log log.T) (err error) {
	return s.handleKeyboardInput(log)
}

// ProcessStreamMessagePayload prints payload received on datachannel to console
func (s *ssmSession) ProcessStreamMessagePayload(log log.T, outputMessage message.ClientMessage) (isHandlerReady bool, err error) {
	fmt.Fprint(s.sshChannel, string(outputMessage.Payload))
	return true, nil
}

// handleKeyboardInput handles input entered by customer on terminal
func (s *ssmSession) handleKeyboardInput(log log.T) (err error) {
	var (
		stdinBytesLen int
	)

	stdinBytes := make([]byte, StdinBufferLimit)
	for {
		if stdinBytesLen, err = s.sshChannel.Read(stdinBytes); err != nil {
			log.Errorf("Unable read from Stdin: %v", err)
			break
		}

		if err = s.Session.DataChannel.SendInputDataMessage(log, message.Output, stdinBytes[:stdinBytesLen]); err != nil {
			log.Errorf("Failed to send UTF8 char: %v", err)
			break
		}
		// sleep to limit the rate of data transfer
		time.Sleep(time.Millisecond)
	}
	return
}

func (s *ssmSession) ResumeSessionHandler(log log.T) (err error) {
	s.TokenValue, err = s.GetResumeSessionParams(log)
	if err != nil {
		log.Errorf("Failed to get token: %v", err)
		return
	} else if s.TokenValue == "" {
		return
	}
	s.DataChannel.GetWsChannel().SetChannelToken(s.TokenValue)
	err = s.DataChannel.Reconnect(log)
	return
}

func (s *ssmSession) OpenDataChannel(log log.T) (err error) {
	s.retryParams = retry.RepeatableExponentialRetryer{
		GeometricRatio:      ssmConfig.RetryBase,
		InitialDelayInMilli: rand.Intn(ssmConfig.DataChannelRetryInitialDelayMillis) + ssmConfig.DataChannelRetryInitialDelayMillis,
		MaxDelayInMilli:     ssmConfig.DataChannelRetryMaxIntervalMillis,
		MaxAttempts:         ssmConfig.DataChannelNumMaxRetries,
	}

	s.DataChannel.Initialize(log, s.ClientId, s.SessionId, s.TargetId, s.IsAwsCliUpgradeNeeded)
	s.DataChannel.SetWebsocket(log, s.StreamUrl, s.TokenValue)
	s.DataChannel.GetWsChannel().SetOnMessage(
		func(input []byte) {
			s.DataChannel.OutputMessageHandler(log, s.Stop, s.SessionId, input)
		})
	s.DataChannel.RegisterOutputStreamHandler(s.ProcessFirstMessage, false)

	if err = s.DataChannel.Open(log); err != nil {
		log.Errorf("Retrying connection for data channel id: %s failed with error: %s", s.SessionId, err)
		s.retryParams.CallableFunc = func() (err error) { return s.DataChannel.Reconnect(log) }
		if err = s.retryParams.Call(); err != nil {
			log.Error(err)
		}
	}

	s.DataChannel.GetWsChannel().SetOnError(
		func(_ error) {
			log.Errorf("Trying to reconnect the session: %v with seq num: %d", s.StreamUrl, s.DataChannel.GetStreamDataSequenceNumber())
			s.retryParams.CallableFunc = func() (err error) { return s.ResumeSessionHandler(log) }
			if err := s.retryParams.Call(); err != nil {
				log.Error(err)
			}
		})

	// Scheduler for resending of data
	s.DataChannel.ResendStreamDataMessageScheduler(log)

	return nil
}

func (s *ssmSession) handleWindowChange(width, height int) error {
	sizeData := message.SizeData{
		Cols: uint32(width),
		Rows: uint32(height),
	}

	var inputSizeData []byte
	var err error

	if inputSizeData, err = json.Marshal(sizeData); err != nil {
		return fmt.Errorf("cannot marshall size data: %v", err)
	}

	if err := s.DataChannel.SendInputDataMessage(log.Logger(false, "border0"), message.Size, inputSizeData); err != nil {
		return fmt.Errorf("failed to Send size data: %v", err)
	}

	return nil
}
