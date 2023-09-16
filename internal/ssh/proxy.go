package ssh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"go.uber.org/zap"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/session-manager-plugin/src/datachannel"
	ssmLog "github.com/aws/session-manager-plugin/src/log"
	"github.com/aws/session-manager-plugin/src/message"
	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/borderzero/border0-cli/internal/service/policy"
	"github.com/borderzero/border0-cli/internal/util"
	"github.com/borderzero/border0-go/types/common"
	"github.com/manifoldco/promptui"
	"golang.org/x/crypto/ssh"
)

const (
	ResizeSleepInterval = 500 * time.Millisecond
	sshProxyVersion     = "SSH-2.0-Border0.com"
)

type ProxyConfig struct {
	Username           string
	Password           string
	IdentityFile       string
	IdentityPrivateKey []byte
	Hostname           string
	Port               int
	sshClientConfig    *ssh.ClientConfig
	sshServerConfig    *ssh.ServerConfig
	AwsSSMTarget       string
	AwsEC2InstanceId   string
	ssmClient          *ssm.Client
	windowWidth        int
	windowHeight       int
	session            *ShellSession
	AWSRegion          string
	AWSProfile         string
	ECSSSMProxy        *ECSSSMProxy
	awsConfig          aws.Config
	AwsUpstreamType    string
	Logger             *zap.Logger
	AwsCredentials     *common.AwsCredentials
	Recording          bool
	EndToEndEncryption bool
	Hostkey            *ssh.Signer
	orgSshCA           ssh.PublicKey
	socket             *models.Socket
	services           services
}

type session struct {
	metadata    border0.E2EEncryptionMetadata
	proxyConfig ProxyConfig
}

type services struct {
	policyService policy.PolicyService
}

type ECSSSMProxy struct {
	Cluster    string
	Services   []string
	Tasks      []string
	Containers []string
}

func BuildProxyConfig(logger *zap.Logger, socket models.Socket, AWSRegion, AWSProfile string, hostkey *ssh.Signer, org *models.Organization, policyService policy.PolicyService) (*ProxyConfig, error) {
	if socket.ConnectorLocalData == nil {
		return nil, nil
	}

	isNormalSSHSocket := socket.UpstreamType != "aws-ssm" && socket.UpstreamType != "aws-ec2connect" && !socket.ConnectorLocalData.AWSEC2InstanceConnectEnabled && !socket.EndToEndEncryptionEnabled
	if isNormalSSHSocket {
		// For connector v2 sockets CAN have an upsream username set
		// so the check below would not pass - so we add this new one.
		if socket.IsBorder0Certificate {
			return nil, nil
		}
		if socket.ConnectorLocalData.UpstreamUsername == "" && socket.ConnectorLocalData.UpstreamPassword == "" {
			if len(socket.ConnectorLocalData.UpstreamIdentityPrivateKey) == 0 && socket.ConnectorLocalData.UpstreamIdentifyFile == "" {
				return nil, nil
			}
		}
	}

	if socket.UpstreamType == "aws-ssm" && socket.ConnectorLocalData.AWSECSCluster == "" && socket.ConnectorLocalData.AwsEC2InstanceId == "" {
		return nil, fmt.Errorf("aws_ecs_cluster or aws ec2 instance id is required for aws-ssm upstream type")
	}

	if socket.UpstreamType == "aws-ssm" && socket.ConnectorLocalData.AWSECSCluster != "" && socket.ConnectorLocalData.AwsEC2InstanceId != "" {
		return nil, fmt.Errorf("aws_ecs_cluster and aws ec2 instance id are mutually exclusive")
	}

	if socket.UpstreamType != "aws-ssm" && !socket.ConnectorLocalData.AWSEC2InstanceConnectEnabled && (socket.ConnectorLocalData.AWSECSCluster != "" || socket.ConnectorLocalData.AwsEC2InstanceId != "") {
		return nil, fmt.Errorf("aws_ecs_cluster or aws ec2 instance id is defined but upstream_type is not aws-ssm")
	}

	if socket.UpstreamType == "aws-ec2connect" || socket.ConnectorLocalData.AWSEC2InstanceConnectEnabled {
		if socket.ConnectorLocalData.AwsEC2InstanceId == "" {
			return nil, fmt.Errorf("aws ec2 instance id is required for aws-ec2connect upstream type")
		}
	}

	proxyConfig := &ProxyConfig{
		Logger:             logger,
		Hostname:           socket.ConnectorData.TargetHostname,
		Port:               socket.ConnectorData.Port,
		Username:           socket.ConnectorLocalData.UpstreamUsername,
		Password:           socket.ConnectorLocalData.UpstreamPassword,
		IdentityFile:       socket.ConnectorLocalData.UpstreamIdentifyFile,
		IdentityPrivateKey: socket.ConnectorLocalData.UpstreamIdentityPrivateKey,
		AwsEC2InstanceId:   socket.ConnectorLocalData.AwsEC2InstanceId,
		AwsSSMTarget:       socket.ConnectorLocalData.AwsEC2InstanceId, // when instance id empty and ecs cluster is given, target will be constructed during connection
		AWSRegion:          AWSRegion,
		AWSProfile:         AWSProfile,
		AwsCredentials:     socket.ConnectorLocalData.AwsCredentials,
		Recording:          socket.RecordingEnabled,
		EndToEndEncryption: socket.EndToEndEncryptionEnabled,
		socket:             &socket,
		services: services{
			policyService: policyService,
		},
	}

	switch {
	case socket.UpstreamType == "aws-ssm":
		proxyConfig.AwsUpstreamType = "aws-ssm"
	case socket.UpstreamType == "aws-ec2connect" || socket.ConnectorLocalData.AWSEC2InstanceConnectEnabled:
		proxyConfig.AwsUpstreamType = "aws-ec2connect"
	}

	if socket.UpstreamType == "aws-ssm" && socket.ConnectorLocalData.AWSECSCluster != "" {
		// TODO: when ECSSSMProxy is not nil, proxyConfig.AwsSSMTarget will be constructed
		// from ecs cluster during connection... this kind of sucks... improve
		proxyConfig.ECSSSMProxy = &ECSSSMProxy{
			Cluster:    socket.ConnectorLocalData.AWSECSCluster,
			Services:   socket.ConnectorLocalData.AWSECSServices,
			Tasks:      socket.ConnectorLocalData.AWSECSTasks,
			Containers: socket.ConnectorLocalData.AWSECSContainers,
		}
	}

	if socket.EndToEndEncryptionEnabled {
		proxyConfig.Hostkey = hostkey
		if orgSshCA, ok := org.Certificates["ssh_public_key"]; ok {
			orgCa, _, _, _, err := ssh.ParseAuthorizedKey([]byte(orgSshCA))
			if err != nil {
				return nil, err
			}

			proxyConfig.orgSshCA = orgCa
		}
	}

	return proxyConfig, nil
}

func Proxy(l net.Listener, c ProxyConfig) error {
	var handler func(net.Conn, ProxyConfig)

	if c.AwsUpstreamType != "" {

		// Use the aws profile from the top level config only
		// if the AwsCredentials object does not have an aws
		// profile defined. The aws profile on the aws creds
		// object comes from socket upstream configuration, so
		// it has higher priority than the aws profile defined
		// in the connector's configuration.
		if c.AWSProfile != "" {
			if c.AwsCredentials == nil {
				c.AwsCredentials = &common.AwsCredentials{}
			}
			if c.AwsCredentials.AwsProfile == nil {
				c.AwsCredentials.AwsProfile = &c.AWSProfile
			}
		}

		cfg, err := util.GetAwsConfig(context.Background(), c.AWSRegion, c.AwsCredentials)
		if err != nil {
			return fmt.Errorf("failed to initialize AWS client: %v", err)
		}
		c.awsConfig = *cfg
	}

	switch c.AwsUpstreamType {
	case "aws-ssm":
		c.ssmClient = ssm.NewFromConfig(c.awsConfig)

		if c.ECSSSMProxy != nil {
			ecsSvc := ecs.NewFromConfig(c.awsConfig)

			input := &ecs.ListTasksInput{
				Cluster:       &c.ECSSSMProxy.Cluster,
				DesiredStatus: types.DesiredStatusRunning,
			}

			output, err := ecsSvc.ListTasks(context.TODO(), input)
			if err != nil {
				return fmt.Errorf("unable to gather ECS cluster tasks, %v", err)
			}

			if len(output.TaskArns) == 0 {
				c.Logger.Sugar().Infof("sshauthproxy: no running tasks found in ECS cluster %s", c.ECSSSMProxy.Cluster)
			}
		}

		handler = handleSsmClient
	case "aws-ec2connect":
		c.sshClientConfig = &ssh.ClientConfig{
			User:            c.Username,
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         5 * time.Second,
		}

		handler = handleEc2InstanceConnectClient
	default:
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

		if len(c.IdentityPrivateKey) > 0 {
			signer, err := ssh.ParsePrivateKey(c.IdentityPrivateKey)
			if err != nil {
				return fmt.Errorf("sshauthproxy: failed to parse identity private key: %s", err)
			}

			authMethods = append(authMethods, ssh.PublicKeys(signer))
		}

		if c.Password != "" {
			authMethods = append(authMethods, ssh.Password(c.Password))
		}

		if len(authMethods) == 0 && !c.EndToEndEncryption {
			return fmt.Errorf("sshauthproxy: no authentication methods provided")
		}

		c.sshClientConfig = &ssh.ClientConfig{
			User:            c.Username,
			Auth:            authMethods,
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         5 * time.Second,
		}

		handler = handleSshClient
	}

	if c.EndToEndEncryption {
		c.sshServerConfig = &ssh.ServerConfig{
			ServerVersion: sshProxyVersion,
		}
	} else {
		c.sshServerConfig = &ssh.ServerConfig{
			NoClientAuth: true,
		}
	}

	if c.Hostkey == nil {
		_, privateKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return fmt.Errorf("sshauthproxy: failed to generate private key: %s", err)
		}

		signer, err := ssh.NewSignerFromKey(privateKey)
		if err != nil {
			return fmt.Errorf("sshauthproxy: failed to generate signer: %s", err)
		}
		c.sshServerConfig.AddHostKey(signer)
	} else {
		c.sshServerConfig.AddHostKey(*c.Hostkey)
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			c.Logger.Error("sshauthproxy: failed to accept connection", zap.Error(err))
			continue
		}

		go func() {
			if c.EndToEndEncryption {
				e2EEncryptionConn, ok := conn.(border0.E2EEncryptionConn)
				if !ok {
					conn.Close()
					c.Logger.Error("failed to cast connection to e2eencryption")
					return
				}

				session := &session{
					metadata:    *e2EEncryptionConn.Metadata,
					proxyConfig: c,
				}

				c.sshServerConfig.PublicKeyCallback = session.sshPublicKeyCallback
				c.sshServerConfig.AuthLogCallback = session.sshAuthLogCallback
			}

			handler(conn, c)
		}()
	}
}

func (s *session) sshPublicKeyCallback(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	cert, ok := key.(*ssh.Certificate)
	if !ok {
		return nil, errors.New("can not cast certificate")
	}

	if s.proxyConfig.orgSshCA == nil {
		return nil, errors.New("error: unable to validate certificate, no CA configured")
	}

	if bytes.Equal(cert.SignatureKey.Marshal(), s.proxyConfig.orgSshCA.Marshal()) {
	} else {
		return nil, errors.New("error: invalid client certificate")
	}

	if s.metadata.UserEmail != cert.KeyId {
		return nil, errors.New("error: ssh certificate does not match tls certificate")
	}

	var certChecker ssh.CertChecker
	if err := certChecker.CheckCert("mysocket_ssh_signed", cert); err != nil {
		return nil, fmt.Errorf("error: invalid client certificate: %s", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ok, err := s.proxyConfig.services.policyService.Authorize(ctx, s.proxyConfig.socket, s.metadata)
	if err != nil {
		return nil, fmt.Errorf("error: failed to authorize: %s", err)
	}

	if !ok {
		// proxy.sessionLogSvc.LogSession(proxy.id, proxy.socket, conn.RemoteAddr(), "denied", authInfo, conn.User(), cert.KeyId, cert.Permissions.Extensions[userinfo_extension])
		return nil, errors.New("error: authorization failed")
	}

	// proxy.sessionLogSvc.LogSession(proxy.id, proxy.socket, conn.RemoteAddr(), "denied", authInfo, conn.User(), cert.KeyId, cert.Permissions.Extensions[userinfo_extension])

	return &ssh.Permissions{}, nil
}

func (s *session) sshAuthLogCallback(conn ssh.ConnMetadata, method string, err error) {
	if err != nil {
		if errors.Is(err, ssh.ErrNoAuth) {
			return
		}
		s.proxyConfig.Logger.Debug("sshauthproxy: authentication failed", zap.String("method", method), zap.String("user", conn.User()), zap.Error(err))
	} else {
		s.proxyConfig.Logger.Debug("sshauthproxy: authentication successful", zap.String("method", method), zap.String("user", conn.User()), zap.String("remote_addr", s.metadata.ClientIP), zap.String("userEmail", s.metadata.UserEmail))
	}
}

func handleSsmClient(conn net.Conn, config ProxyConfig) {
	defer conn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, config.sshServerConfig)
	if err != nil {
		config.Logger.Sugar().Errorf("failed to accept ssh connection: %s", err)
		return
	}

	defer sshConn.Close()

	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel == nil {
			config.Logger.Sugar().Errorf("proxy channel closed")
			return
		}

		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			config.Logger.Sugar().Errorf("failed to accept channel: %s", err)
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
						if err := pickAwsEcsTargetForAwsSsm(channel, &config); err != nil {
							config.Logger.Sugar().Errorf("failed to pick ECS target: %s", err)
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

func pickAwsEcsTargetForAwsSsm(channel ssh.Channel, proxyConfig *ProxyConfig) error {
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

	proxyConfig.AwsSSMTarget = fmt.Sprintf("ecs:%s_%s_%s", selectedCluster, tasksIDS[selectedTask], containers[selectedTask][selectedContainer])

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
		Target: &config.AwsSSMTarget,
	})
	if err != nil {
		config.Logger.Sugar().Errorf("failed to start ssm session: %s\n", err)
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
		config.Logger.Sugar().Errorf("failed to execute ssm session: %s\n", err)
		return
	}

	defer s.TerminateSession(sessionLogger)

	s.SessionType = "border0"
	s.sshChannel = channel

	s.Initialize(sessionLogger, &s)
	if s.SetSessionHandlers(sessionLogger); err != nil {
		config.Logger.Sugar().Errorf("failed to execute ssm session: %s\n", err)
	}
}

func handleSshClient(conn net.Conn, config ProxyConfig) {
	defer conn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, config.sshServerConfig)
	if err != nil {
		config.Logger.Sugar().Errorf("failed to accept ssh connection: %s", err)
		return
	}

	go ssh.DiscardRequests(reqs)
	if err := handleChannels(sshConn, chans, config); err != nil {
		config.Logger.Sugar().Errorf("failed to handle channels: %s", err)
		return
	}
}

func handleEc2InstanceConnectClient(conn net.Conn, config ProxyConfig) {
	defer conn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, config.sshServerConfig)
	if err != nil {
		config.Logger.Sugar().Errorf("failed to accept ssh connection: %s", err)
		return
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		config.Logger.Sugar().Errorf("failed to generate key: %s\n", err)
		return
	}

	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	})

	signer, err := ssh.ParsePrivateKey(privateKeyPEM)
	if err != nil {
		config.Logger.Sugar().Errorf("unable to parse private key: %s\n", err)
		return
	}

	config.sshClientConfig.Auth = []ssh.AuthMethod{
		ssh.PublicKeys(signer),
	}

	sshConnUser := sshConn.User()

	// We only use the user from the ssh connection if
	// the ssh client config does not have a user defined.
	// If the user in the ssh client config at this point
	// is not empty string, then it came from the socket's
	// upstream configuration (so we use that).
	if config.sshClientConfig.User == "" {
		config.sshClientConfig.User = sshConnUser
	}

	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		config.Logger.Sugar().Errorf("unable to generate public key: %s\n", err)
		return
	}

	publicKeyBytes := ssh.MarshalAuthorizedKey(publicKey)
	publicKeyString := string(publicKeyBytes)

	ec2ConnectClient := ec2instanceconnect.NewFromConfig(config.awsConfig)
	_, err = ec2ConnectClient.SendSSHPublicKey(context.TODO(), &ec2instanceconnect.SendSSHPublicKeyInput{
		InstanceId:     &config.AwsEC2InstanceId,
		InstanceOSUser: &config.sshClientConfig.User,
		SSHPublicKey:   &publicKeyString,
	})

	if err != nil {
		config.Logger.Sugar().Errorf("failed to send ssh public key: %s\n", err)
		return
	}

	go ssh.DiscardRequests(reqs)
	if err := handleChannels(sshConn, chans, config); err != nil {
		config.Logger.Sugar().Errorf("failed to handle channels: %s\n", err)
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

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer clientChannel.CloseWrite()
		defer wg.Done()
		io.Copy(clientChannel, serverChannel)
		cancel()
	}()

	go func() {
		defer serverChannel.CloseWrite()
		defer wg.Done()
		io.Copy(serverChannel, clientChannel)
		cancel()
	}()

	go func() {
		defer wg.Done()
		for {
			select {
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
	}()

	wg.Wait()
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
