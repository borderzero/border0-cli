package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamTypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/session-manager-plugin/src/datachannel"
	ssmLog "github.com/aws/session-manager-plugin/src/log"
	"github.com/aws/session-manager-plugin/src/message"
	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/manifoldco/promptui"
	"golang.org/x/crypto/ssh"
)

const ResizeSleepInterval = 500 * time.Millisecond

type ProxyConfig struct {
	Username        string
	Password        string
	IdentityFile    string
	Hostname        string
	Port            int
	sshClientConfig *ssh.ClientConfig
	sshServerConfig *ssh.ServerConfig
	AwsEC2Target    string
	ssmClient       *ssm.Client
	windowWidth     int
	windowHeight    int
	session         *ShellSession
	AWSRegion       string
	AWSProfile      string
	ECSSSMProxy     *ECSSSMProxy
	awsConfig       aws.Config
}

type ECSSSMProxy struct {
	Cluster    string
	Services   []string
	Tasks      []string
	Containers []string
}

func BuildProxyConfig(socket models.Socket, AWSRegion, AWSProfile string) (*ProxyConfig, error) {
	if socket.ConnectorLocalData.UpstreamUsername == "" && socket.ConnectorLocalData.UpstreamPassword == "" &&
		socket.ConnectorLocalData.UpstreamIdentifyFile == "" && socket.ConnectorLocalData.AWSEC2Target == "" &&
		socket.UpstreamType != "aws-ssm" {
		return nil, nil
	}

	if socket.UpstreamType == "aws-ssm" && socket.ConnectorLocalData.AWSECSCluster == "" {
		return nil, fmt.Errorf("ecs_cluster is required for aws-ssm upstream type")
	}

	proxyConfig := &ProxyConfig{
		Hostname:     socket.ConnectorData.TargetHostname,
		Port:         socket.ConnectorData.Port,
		Username:     socket.ConnectorLocalData.UpstreamUsername,
		Password:     socket.ConnectorLocalData.UpstreamPassword,
		IdentityFile: socket.ConnectorLocalData.UpstreamIdentifyFile,
		AwsEC2Target: socket.ConnectorLocalData.AWSEC2Target,
		AWSRegion:    AWSRegion,
		AWSProfile:   AWSProfile,
	}

	if socket.UpstreamType == "aws-ssm" {
		proxyConfig.ECSSSMProxy = &ECSSSMProxy{
			Cluster:    socket.ConnectorLocalData.AWSECSCluster,
			Services:   socket.ConnectorLocalData.AWSECSServices,
			Tasks:      socket.ConnectorLocalData.AWSECSTasks,
			Containers: socket.ConnectorLocalData.AWSECSContainers,
		}
	}

	return proxyConfig, nil
}

func Proxy(l net.Listener, c ProxyConfig) error {
	var handler func(net.Conn, ProxyConfig)

	if c.AwsEC2Target != "" || c.ECSSSMProxy != nil {
		var awsConfig aws.Config
		var err error

		if c.AWSProfile == "" {
			awsConfig, err = config.LoadDefaultConfig(context.TODO())
		} else {
			awsConfig, err = config.LoadDefaultConfig(context.TODO(),
				config.WithSharedConfigProfile(c.AWSProfile))
		}

		if err != nil {
			return fmt.Errorf("failed to load aws config: %s", err)
		}

		if c.AWSRegion != "" {
			awsConfig.Region = c.AWSRegion
		}

		c.ssmClient = ssm.NewFromConfig(awsConfig)
		c.awsConfig = awsConfig

		if c.ECSSSMProxy != nil {
			ecsSvc := ecs.NewFromConfig(c.awsConfig)
			if err != nil {
				return fmt.Errorf("failed to create ecs client: %s", err)
			}

			input := &ecs.ListTasksInput{
				Cluster:       &c.ECSSSMProxy.Cluster,
				DesiredStatus: types.DesiredStatusRunning,
			}

			output, err := ecsSvc.ListTasks(context.TODO(), input)
			if err != nil {
				return fmt.Errorf("unable to gather ECS cluster tasks, %v", err)
			}

			if len(output.TaskArns) == 0 {
				log.Printf("sshauthproxy: no running tasks found in ECS cluster %s", c.ECSSSMProxy.Cluster)
			}

			stsClient := sts.NewFromConfig(c.awsConfig)
			resp, err := stsClient.GetCallerIdentity(context.TODO(), &sts.GetCallerIdentityInput{})
			if err != nil {
				return fmt.Errorf("failed to get caller identity %s", err)
			}

			actualRoleArn := *resp.Arn
			if strings.Contains(*resp.Arn, ":assumed-role/") {
				roleArnParts := strings.Split(*resp.Arn, ":")
				accountID := roleArnParts[4]
				roleName := strings.Split(roleArnParts[5], "/")[1]
				actualRoleArn = fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, roleName)
			}

			params := &iam.SimulatePrincipalPolicyInput{
				PolicySourceArn: aws.String(actualRoleArn),
				ActionNames:     []string{"ssm:StartSession"},
			}

			iamSvc := iam.NewFromConfig(c.awsConfig)
			simulateResponse, err := iamSvc.SimulatePrincipalPolicy(context.TODO(), params)
			if err != nil {
				return fmt.Errorf("error simulating principal policy %s", err)
			}

			allowed := false
			for _, evaluationResults := range simulateResponse.EvaluationResults {
				if evaluationResults.EvalDecision == iamTypes.PolicyEvaluationDecisionTypeAllowed {
					allowed = true
					break
				}
			}

			if !allowed {
				return fmt.Errorf("user (%s) is not authorized to start a session", *resp.Account)
			}
		}

		handler = handelSSMclient
	} else {
		var authMethods []ssh.AuthMethod
		if c.IdentityFile != "" {
			bytes, err := os.ReadFile(c.IdentityFile)
			if err != nil {
				return fmt.Errorf("sshauthproxy: failed to read identity file: %s", err)
			}

			signer, err := ssh.ParsePrivateKey(bytes)
			if err != nil {
				return fmt.Errorf("sshauthproxy: failed to parse identity file: %s", err)
			}

			authMethods = append(authMethods, ssh.PublicKeys(signer))
		}

		if c.Password != "" {
			authMethods = append(authMethods, ssh.Password(c.Password))
		}

		if len(authMethods) == 0 {
			return fmt.Errorf("sshauthproxy: no authentication methods provided")
		}

		c.sshClientConfig = &ssh.ClientConfig{
			User:            c.Username,
			Auth:            authMethods,
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         5 * time.Second,
		}

		handler = handleSSHclient
	}

	c.sshServerConfig = &ssh.ServerConfig{
		NoClientAuth: true,
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("sshauthproxy: failed to generate private key: %s", err)
	}

	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return fmt.Errorf("sshauthproxy: failed to generate signer: %s", err)
	}
	c.sshServerConfig.AddHostKey(signer)

	for {
		conn, err := l.Accept()
		if err != nil {
			return fmt.Errorf("sshauthproxy: failed to accept connection: %s", err)
		}

		go handler(conn, c)
	}
}

func handelSSMclient(conn net.Conn, config ProxyConfig) {
	defer conn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, config.sshServerConfig)
	if err != nil {
		fmt.Printf("sshauthproxy: failed to accept ssh connection: %s", err)
		return
	}

	defer sshConn.Close()

	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel == nil {
			fmt.Printf("sshauthproxy: proxy channel closed")
			return
		}

		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			fmt.Printf("sshauthproxy: failed to accept channel: %s", err)
			return
		}

		go func(in <-chan *ssh.Request) {
			for req := range in {
				switch {
				case req == nil:
					continue
				case req.Type == "pty-req":
					termLen := req.Payload[3]
					w, h := parseDims(req.Payload[termLen+4:])
					config.windowWidth = int(w)
					config.windowHeight = int(h)
					req.Reply(true, nil)
				case req.Type == "window-change":
					w, h := parseDims(req.Payload)
					config.session.handleWindowChange(int(w), int(h))
					req.Reply(false, nil)
				case req.Type == "shell":
					if config.ECSSSMProxy != nil {
						if err := pickAWSECSTarget(channel, &config); err != nil {
							log.Printf("sshauthproxy: failed to pick ECS target: %s", err)
							req.Reply(false, nil)
							sshConn.Close()
							return
						}
					}
					go handleSSMShell(channel, &config)
					req.Reply(true, nil)
				default:
					req.Reply(false, nil)
				}
			}
		}(requests)
	}

}

func pickAWSECSTarget(channel ssh.Channel, proxyConfig *ProxyConfig) error {
	ecsSvc := ecs.NewFromConfig(proxyConfig.awsConfig)
	var selectedCluster string
	if proxyConfig.ECSSSMProxy.Cluster == "" {
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
		selectedCluster = proxyConfig.ECSSSMProxy.Cluster
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
		if len(proxyConfig.ECSSSMProxy.Tasks) > 0 {
			var found bool
			for _, c := range proxyConfig.ECSSSMProxy.Tasks {
				if strings.HasPrefix(strings.ToLower(taskName), strings.ToLower(c)) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		if len(proxyConfig.ECSSSMProxy.Services) > 0 {
			service := strings.TrimPrefix(*task.Group, "service:")
			var found bool
			for _, c := range proxyConfig.ECSSSMProxy.Services {
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
			if len(proxyConfig.ECSSSMProxy.Containers) > 0 {
				var found bool
				for _, c := range proxyConfig.ECSSSMProxy.Containers {
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

	proxyConfig.AwsEC2Target = fmt.Sprintf("ecs:%s_%s_%s", selectedCluster, tasksIDS[selectedTask], containers[selectedTask][selectedContainer])

	return nil
}

type ssmDataChannel struct {
	datachannel.DataChannel
}

func (s ssmDataChannel) HandleChannelClosedMessage(log ssmLog.T, stopHandler datachannel.Stop, sessionId string, outputMessage message.ClientMessage) {
	stopHandler()
}

func (dataChannel *ssmDataChannel) OutputMessageHandler(log ssmLog.T, stopHandler datachannel.Stop, sessionID string, rawMessage []byte) error {
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

func handleSSMShell(channel ssh.Channel, config *ProxyConfig) {
	defer channel.Close()

	var s ShellSession
	sessionOutput, err := config.ssmClient.StartSession(context.TODO(), &ssm.StartSessionInput{
		Target: &config.AwsEC2Target,
	})
	if err != nil {
		fmt.Printf("sshauthproxy: failed to start ssm session: %s\n", err)
		return
	}

	datachannel := ssmDataChannel{
		DataChannel: datachannel.DataChannel{},
	}

	s.SessionId = *sessionOutput.SessionId
	s.StreamUrl = *sessionOutput.StreamUrl
	s.TokenValue = *sessionOutput.TokenValue
	s.DataChannel = &datachannel
	config.session = &s
	s.handleWindowChange(config.windowWidth, config.windowHeight)
	sessionLogger := ssmLog.Logger(false, "border0")

	if err = s.OpenDataChannel(sessionLogger); err != nil {
		fmt.Printf("sshauthproxy: failed to execute ssm session: %s\n", err)
		return
	}

	defer s.TerminateSession(sessionLogger)

	s.SessionType = "border0"
	s.sshChannel = channel

	s.Initialize(sessionLogger, &s)
	if s.SetSessionHandlers(sessionLogger); err != nil {
		fmt.Printf("sshauthproxy: failed to execute ssm session: %s\n", err)
	}
}

func handleSSHclient(conn net.Conn, config ProxyConfig) {
	defer conn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, config.sshServerConfig)
	if err != nil {
		fmt.Printf("sshauthproxy: failed to accept ssh connection: %s\n", err)
		return
	}

	go ssh.DiscardRequests(reqs)
	if err := handleChannels(sshConn, chans, config); err != nil {
		fmt.Printf("sshauthproxy: failed to handle channels: %s\n", err)
		return
	}
}

func handleChannels(sshConn *ssh.ServerConn, chans <-chan ssh.NewChannel, config ProxyConfig) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer sshConn.Close()

	upstreamConn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", config.Hostname, config.Port), 5*time.Second)
	if err != nil {
		return fmt.Errorf("unable to connect to upstream host: %s", err)
	}

	sshClientConn, clientChans, req, err := ssh.NewClientConn(upstreamConn, "", config.sshClientConfig)
	if err != nil {
		return fmt.Errorf("unable to connect to upstream host: %s", err)
	}

	go ssh.DiscardRequests(req)

	defer sshClientConn.Close()

	for {
		select {
		case newChannel := <-chans:
			if newChannel == nil {
				return nil
			}

			go handleChannel(ctx, cancel, newChannel, sshClientConn)
		case newChannel := <-clientChans:
			if newChannel == nil {
				return fmt.Errorf("upstream channel closed")
			}

			go handleChannel(ctx, cancel, newChannel, sshConn.Conn)
		case <-ctx.Done():
			return nil
		}
	}
}

func handleChannel(ctx context.Context, cancel context.CancelFunc, newChannel ssh.NewChannel, sshConn ssh.Conn) {
	clientChannel, clientReq, err := sshConn.OpenChannel(newChannel.ChannelType(), newChannel.ExtraData())
	if err != nil {
		if chanErr, ok := err.(*ssh.OpenChannelError); ok {
			if err := newChannel.Reject(chanErr.Reason, chanErr.Message); err != nil {
				return
			}
		} else {
			if err = newChannel.Reject(ssh.ConnectionFailed, fmt.Sprintf("error connecting to backend (%s)", err)); err != nil {
				return
			}
		}
		return
	}

	serverChannel, serverReq, err := newChannel.Accept()
	if err != nil {
		return
	}

	defer clientChannel.Close()
	defer serverChannel.Close()

	go func() {
		io.Copy(clientChannel, serverChannel)
		cancel()
	}()

	go func() {
		io.Copy(serverChannel, clientChannel)
		cancel()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case req := <-clientReq:
			if req == nil {
				return
			}

			handleRequest(serverChannel, req)
		case req := <-serverReq:
			if req == nil {
				return
			}

			handleRequest(clientChannel, req)
		}
	}
}

func handleRequest(channel ssh.Channel, req *ssh.Request) {
	ok, _ := channel.SendRequest(req.Type, req.WantReply, req.Payload)

	if req.WantReply {
		req.Reply(ok, nil)
	}
}

func parseDims(b []byte) (uint32, uint32) {
	w := binary.BigEndian.Uint32(b)
	h := binary.BigEndian.Uint32(b[4:])
	return w, h
}
