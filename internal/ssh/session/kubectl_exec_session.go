package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	awsv1 "github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	sessionv1 "github.com/aws/aws-sdk-go/aws/session"
	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/border0"
	sshConfig "github.com/borderzero/border0-cli/internal/ssh/config"
	"github.com/borderzero/border0-go/lib/types/maps"
	"github.com/borderzero/border0-go/lib/types/set"
	"github.com/borderzero/border0-go/lib/types/slice"
	"github.com/manifoldco/promptui"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/aws-iam-authenticator/pkg/token"
)

type kubectlExecSessionHandler struct {
	logger      *zap.Logger
	proxyConfig *sshConfig.ProxyConfig
	kubeConfig  *rest.Config
}

// ensure kubectlExecSessionHandler implements SessionHandler.
var _ SessionHandler = (*kubectlExecSessionHandler)(nil)

type kubectlExecSession struct {
	logger      *zap.Logger
	proxyConfig *sshConfig.ProxyConfig

	e2eeMetadata *border0.E2EEncryptionMetadata

	sshServerConfig *ssh.ServerConfig
	sshHeight       int
	sshWidth        int

	// active channels
	downstreamSshConn  *ssh.ServerConn
	downstreamSshChans <-chan ssh.NewChannel
}

func NewKubectlExecSessionHandler(
	logger *zap.Logger,
	proxyConfig *sshConfig.ProxyConfig,
) *kubectlExecSessionHandler {
	return &kubectlExecSessionHandler{
		logger:      logger,
		proxyConfig: proxyConfig,
	}
}

// Proxy runs the local proxying function between the connection to the remote Border0 proxy and
// the origin service (in this case the origin service is a connection to a remote kubectl executor).
func (s *kubectlExecSessionHandler) Proxy(conn net.Conn) {
	defer conn.Close()

	ctx := context.Background() // FIXME

	k8sSess := &kubectlExecSession{
		logger:          s.logger,
		proxyConfig:     s.proxyConfig,
		sshServerConfig: s.proxyConfig.SshServerConfig,
		sshWidth:        80,
		sshHeight:       24,
	}

	if s.proxyConfig.EndToEndEncryption {
		e2EEncryptionConn, ok := conn.(border0.E2EEncryptionConn)
		if !ok {
			conn.Close()
			s.logger.Error("failed to cast connection to e2eencryption")
			return
		}
		if e2EEncryptionConn.Metadata == nil {
			s.logger.Error("invalid e2e metadata")
			return
		}
		k8sSess.e2eeMetadata = e2EEncryptionConn.Metadata
		k8sSess.logger = k8sSess.logger.With(zap.String("session_key", k8sSess.e2eeMetadata.SessionKey))
		// set the ssh server config's callback to the method on the kubectlExecSession
		k8sSess.sshServerConfig.PublicKeyCallback = k8sSess.publicKeyCallback
	}

	// accept SSH connection from Border0 proxy
	dsConn, dsChanns, dsReqs, err := ssh.NewServerConn(conn, k8sSess.proxyConfig.SshServerConfig)
	if err != nil {
		k8sSess.logger.Error("failed to accept ssh connection from upstream proxy", zap.Error(err))
		return
	}
	k8sSess.downstreamSshConn = dsConn
	k8sSess.downstreamSshChans = dsChanns

	// we don't support global requests (yet)
	// so we can disregard the reqs channel
	go ssh.DiscardRequests(dsReqs)

	if k8sSess.proxyConfig.EndToEndEncryption {
		username := k8sSess.downstreamSshConn.User()

		if s.proxyConfig.Username != "" {
			username = s.proxyConfig.Username
		}

		if err := k8sSess.proxyConfig.Border0API.UpdateSession(models.SessionUpdate{
			SessionKey: k8sSess.e2eeMetadata.SessionKey,
			Socket:     k8sSess.proxyConfig.Socket,
			UserData:   ",sshuser=" + username,
		}); err != nil {
			k8sSess.logger.Error("failed to update session", zap.Error(err))
			return
		}
	}

	if err := k8sSess.handleChannels(ctx); err != nil {
		s.logger.Error("failed to handle channels", zap.Error(err))
		return
	}
}

func (s *kubectlExecSession) handleChannels(ctx context.Context) error {
	defer s.downstreamSshConn.Close()

	channelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

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

		termSizeQueueChan := make(chan *remotecommand.TerminalSize, 50)
		termSizeQ := &terminalWindowSizeQueue{
			ctx: channelCtx,
			c:   termSizeQueueChan,
		}

		go func(in <-chan *ssh.Request) {
			for req := range in {
				switch {
				case req == nil:
					continue
				// handled mostly for the benefit of session recordings
				case req.Type == "pty-req":
					termLen := req.Payload[3]
					w, h := parseDims(req.Payload[termLen+4:])
					s.sshWidth = int(w)
					s.sshHeight = int(h)
					termSizeQueueChan <- &remotecommand.TerminalSize{
						Width:  uint16(w),
						Height: uint16(h),
					}
					if req.WantReply {
						req.Reply(true, nil)
					}
				// handled mostly for the benefit of session recordings
				case req.Type == "window-change":
					w, h := parseDims(req.Payload)
					s.sshWidth = int(w)
					s.sshHeight = int(h)
					termSizeQueueChan <- &remotecommand.TerminalSize{
						Width:  uint16(w),
						Height: uint16(h),
					}
					if req.WantReply {
						req.Reply(true, nil)
					}
				case req.Type == "shell":
					if req.WantReply {
						req.Reply(true, nil)
					}
					go s.handleChannel(channelCtx, channel, termSizeQ, s.downstreamSshConn.User())
				default:
					req.Reply(false, nil)
				}
			}
		}(requests)
	}

	return nil
}

func (s *kubectlExecSession) getKubeconfig(ctx context.Context) (*rest.Config, error) {
	// if its an AWS EKS cluster, we use aws credentials to get kubeconfig
	if s.proxyConfig.KubectlExecProxy.IsAwsEks {
		// get cluster details
		eksClient := eks.NewFromConfig(s.proxyConfig.AwsConfig)
		describeClusterCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		defer cancel()
		describeClusterOutput, err := eksClient.DescribeCluster(describeClusterCtx, &eks.DescribeClusterInput{
			Name: aws.String(s.proxyConfig.KubectlExecProxy.AwsEksClusterName),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to describe eks cluster \"%s\": %v", s.proxyConfig.KubectlExecProxy.AwsEksClusterName, err)
		}

		// initialize new token generator
		iamAuthTokenGenerator, err := token.NewGenerator(true, false)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize aws iam authenticator token generator: %v", err)
		}

		// retrieve credentials to use with the aws go sdk v1. we have to do this because
		// the aws iam authenticator for k8s only interfaces with the legacy aws go sdk (v1).
		retrieveCredsCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		defer cancel()
		v2creds, err := s.proxyConfig.AwsConfig.Credentials.Retrieve(retrieveCredsCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve temporary aws credentials: %v", err)
		}

		// use legacy credentials object to init new session object
		session, err := sessionv1.NewSession(&awsv1.Config{
			Credentials: credentials.NewStaticCredentials(
				v2creds.AccessKeyID,
				v2creds.SecretAccessKey,
				v2creds.SessionToken,
			),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to initialize new aws session: %v", err)
		}

		// retrieve k8s bearer token
		token, err := iamAuthTokenGenerator.GetWithOptions(&token.GetTokenOptions{
			ClusterID:   s.proxyConfig.KubectlExecProxy.AwsEksClusterName,
			Region:      s.proxyConfig.AwsConfig.Region, // OK if empty
			Session:     session,                        // have to pass it or else default credential chain is used
			SessionName: fmt.Sprintf("border0-k8s-%d", time.Now().UnixNano()),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to generate k8s token with aws iam authenticator token generator: %v", err)
		}

		// decode CA data
		ca, err := base64.StdEncoding.DecodeString(aws.ToString(describeClusterOutput.Cluster.CertificateAuthority.Data))
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64-encoded kubernetes cluster CA data: %v", err)
		}

		// build kubeconfig
		kubeConfig := &rest.Config{
			Host:            aws.ToString(describeClusterOutput.Cluster.Endpoint),
			BearerToken:     token.Token,
			TLSClientConfig: rest.TLSClientConfig{CAData: ca},
		}
		return kubeConfig, nil
	}

	// if kubeconfig path is defined, use it
	if s.proxyConfig.KubectlExecProxy.KubeconfigPath != "" {
		kubeConfig, err := clientcmd.BuildConfigFromFlags(
			s.proxyConfig.KubectlExecProxy.MasterUrl, // OK if empty
			s.proxyConfig.KubectlExecProxy.KubeconfigPath,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to build kubeconfig from the given path: %v", err)
		}
		return kubeConfig, nil
	}

	// otherwise use k8s default config loading rules
	kubeConfig, err := clientcmd.BuildConfigFromKubeconfigGetter(
		s.proxyConfig.KubectlExecProxy.MasterUrl, // OK if empty
		clientcmd.NewDefaultClientConfigLoadingRules().GetStartingConfig,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %v", err)
	}
	return kubeConfig, nil

}

type terminalWindowSizeQueue struct {
	ctx context.Context
	c   chan *remotecommand.TerminalSize
}

// ensure terminalWindowSizeQueue implements remotecommand.TerminalSizeQueue.
var _ remotecommand.TerminalSizeQueue = (*terminalWindowSizeQueue)(nil)

func (q *terminalWindowSizeQueue) Next() *remotecommand.TerminalSize {
	select {
	case size := <-q.c:
		return size
	case <-q.ctx.Done():
		close(q.c)
		return nil
	}
}

func (s *kubectlExecSession) handleChannel(
	ctx context.Context,
	channel ssh.Channel,
	terminalSizeQ remotecommand.TerminalSizeQueue,
	user string,
) {
	defer channel.Close()

	channel.Write([]byte("Retrieving temporary kubernetes credentials..."))
	kubeconfig, err := s.getKubeconfig(ctx)
	if err != nil {
		channel.Write([]byte(" ✘\r\n"))
		s.logger.Error("failed to get kubeconfig", zap.Error(err))
		return
	}
	channel.Write([]byte(" ✔\r\n"))

	if s.proxyConfig.IsRecordingEnabled() {
		pwc := NewPipeWriteChannel(channel)
		channel = pwc
		r := NewRecording(s.logger, pwc.reader, s.proxyConfig.Socket.SocketID, s.e2eeMetadata.SessionKey, s.proxyConfig.Border0API, s.sshWidth, s.sshHeight)
		if err := r.Record(); err != nil {
			s.logger.Error("failed to record session", zap.Error(err))
			return
		}
		defer r.Stop()
	}

	target, err := s.askForTarget(ctx, channel, kubeconfig)
	if err != nil {
		s.logger.Error("failed to determine target for remote command executor", zap.Error(err))
		return
	}
	channel.Write([]byte("\n"))

	// we iterate over the slice and not the set
	// because order is not maintained for the set
	shells := []string{"bash", "zsh", "ash", "sh"}
	shellSet := set.New(shells...)
	for _, shell := range shells {
		if shellSet.Size() == 0 {
			channel.Write([]byte("No shells available in the target container :("))
			s.logger.Error("no shells available in the target container", zap.Error(err))
			return
		}

		exec, err := getRemoteCommandExecutor(
			ctx,
			kubeconfig,
			user,
			shell,
			target,
		)
		if err != nil {
			s.logger.Error(
				"failed to get remote command executor for target",
				zap.String("namespace", target.namespace),
				zap.String("pod", target.pod),
				zap.String("container", target.container),
				zap.String("user", user),
				zap.String("shell", shell),
				zap.Error(err),
			)
			return
		}

		err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdin:             channel,
			Stdout:            channel,
			Stderr:            channel.Stderr(),
			Tty:               true,
			TerminalSizeQueue: terminalSizeQ,
		})
		if err != nil {
			if strings.Contains(err.Error(), "executable file not found") ||
				strings.Contains(err.Error(), "command terminated with exit code 127") {
				shellSet.Remove(shell)
				continue // try next shell
			}
			if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
				s.logger.Error(
					"failed to stream between ssh channel and target container",
					zap.String("namespace", target.namespace),
					zap.String("pod", target.pod),
					zap.String("container", target.container),
					zap.String("user", user),
					zap.String("shell", shell),
					zap.Error(err),
				)
			}
		}
		return
	}
}

type kubectlExecTarget struct {
	namespace string
	pod       string
	container string
}

func (s *kubectlExecSession) askForTarget(ctx context.Context, channel ssh.Channel, kubeconfig *rest.Config) (*kubectlExecTarget, error) {
	clientset, err := kubernetes.NewForConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize new k8s clientset to query the cluster: %v", err)
	}

	// get namespaces from k8s api
	listNsCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	namespaceList, err := clientset.CoreV1().Namespaces().List(listNsCtx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces in cluster: %v", err)
	}

	// filter namespaces if there with allowlist *iff* provided, otherwise all are available
	namespaces := []string{}
	if len(s.proxyConfig.KubectlExecProxy.NamespaceAllowlist) == 0 {
		namespaces = slice.Transform(namespaceList.Items, func(ns v1.Namespace) string { return ns.Name })
	} else {
		for _, ns := range namespaceList.Items {
			if slices.Contains(s.proxyConfig.KubectlExecProxy.NamespaceAllowlist, ns.Name) {
				namespaces = append(namespaces, ns.Name)
			}
		}
	}
	if len(namespaces) == 0 {
		channel.Write([]byte("No targets available to you - sorry :("))
		return nil, fmt.Errorf("No (allowlisted) namespaces found in cluster")
	}

	// identify namespace to use
	namespace := ""
	if len(namespaces) == 1 {
		namespace = namespaces[0]
	} else {
		namespacePrompt := promptui.Select{
			Label:             "Choose a namespace",
			Items:             namespaces,
			Stdout:            channel,
			Stdin:             channel,
			StartInSearchMode: true,
			Searcher: func(input string, index int) bool {
				return strings.Contains(strings.ToLower(namespaces[index]), strings.ToLower(input))
			},
		}
		_, selectedNamespace, err := namespacePrompt.Run()
		if err != nil {
			return nil, fmt.Errorf("unable to select namespace: %v", err)
		}
		namespace = selectedNamespace
	}

	// if there is a selectors allowlist for this namespace
	compareChosenPodAgainstSelectors := false
	selectors := map[string][]string{}

	if namespaceSelectors, ok := s.proxyConfig.KubectlExecProxy.NamespaceSelectorsAllowlist[namespace]; ok && len(namespaceSelectors) != 0 {
		compareChosenPodAgainstSelectors = true
		selectors = namespaceSelectors
	}

	// get pods from k8s api
	listPodsCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	podList, err := clientset.CoreV1().Pods(namespace).List(listPodsCtx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods in namespace \"%s\": %v", namespace, err)
	}

	// filter them if needed based on selectors. Otherwise just extract the name.
	pods := []string{}
	for _, pod := range podList.Items {
		if compareChosenPodAgainstSelectors {
			if !maps.MatchesFilters(pod.ObjectMeta.Labels, selectors, nil) {
				continue
			}
		}
		pods = append(pods, pod.Name)
	}
	if len(pods) == 0 {
		channel.Write([]byte("No targets available to you - sorry :("))
		return nil, fmt.Errorf("No (allowlisted) pods found in namespace \"%s\"", namespace)
	}

	// identify pod to use
	pod := ""
	if len(pods) == 1 {
		pod = pods[0]
	} else {
		// pick a pod
		podPrompt := promptui.Select{
			Label:             "Choose a pod",
			Items:             pods,
			Stdout:            channel,
			Stdin:             channel,
			StartInSearchMode: true,
			Searcher: func(input string, index int) bool {
				return strings.Contains(strings.ToLower(pods[index]), strings.ToLower(input))
			},
		}
		_, selectedPod, err := podPrompt.Run()
		if err != nil {
			return nil, fmt.Errorf("unable to select pod: %v", err)
		}
		pod = selectedPod
	}

	// describe selected pod via k8s api
	getPodCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	describedPod, err := clientset.CoreV1().Pods(namespace).Get(getPodCtx, pod, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get containers in pod %s of namespace %s: %v", pod, namespace, err)
	}
	if len(describedPod.Spec.Containers) == 0 {
		channel.Write([]byte("No targets available to you - sorry :("))
		return nil, fmt.Errorf("No containers available in pod \"%s\" of namespace \"%s\"", pod, namespace)
	}

	container := ""
	if len(describedPod.Spec.Containers) == 1 {
		container = describedPod.Spec.Containers[0].Name
	} else {
		// pick a container
		containerChoices := slice.Transform(describedPod.Spec.Containers, func(container v1.Container) string { return container.Name })
		containerPrompt := promptui.Select{
			Label:             "Choose a container",
			Items:             containerChoices,
			Stdout:            channel,
			Stdin:             channel,
			StartInSearchMode: true,
			Searcher: func(input string, index int) bool {
				return strings.Contains(strings.ToLower(containerChoices[index]), strings.ToLower(input))
			},
		}
		_, selectedContainer, err := containerPrompt.Run()
		if err != nil {
			return nil, fmt.Errorf("unable to select container: %v", err)
		}
		container = selectedContainer
	}

	return &kubectlExecTarget{
		namespace: namespace,
		pod:       pod,
		container: container,
	}, nil
}

// FIXME: make generic, ssm uses the exact same code
func (s *kubectlExecSession) publicKeyCallback(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	cert, ok := key.(*ssh.Certificate)
	if !ok {
		return nil, errors.New("can not cast certificate")
	}

	if s.proxyConfig.OrgSshCA == nil {
		return nil, errors.New("error: unable to validate certificate, no CA configured")
	}

	if bytes.Equal(cert.SignatureKey.Marshal(), s.proxyConfig.OrgSshCA.Marshal()) {
	} else {
		return nil, errors.New("error: invalid client certificate")
	}

	if s.e2eeMetadata.UserEmail != cert.KeyId {
		return nil, errors.New("error: ssh certificate does not match tls certificate")
	}

	var certChecker ssh.CertChecker
	if err := certChecker.CheckCert("mysocket_ssh_signed", cert); err != nil {
		return nil, fmt.Errorf("error: invalid client certificate: %s", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	actions, _, err := s.proxyConfig.Border0API.Evaluate(ctx, s.proxyConfig.Socket, s.e2eeMetadata.ClientIP, s.e2eeMetadata.UserEmail, s.e2eeMetadata.SessionKey)
	if err != nil {
		return nil, fmt.Errorf("error: failed to authorize: %s", err)
	}

	if len(actions) == 0 {
		return nil, errors.New("error: authorization failed")
	}

	return &ssh.Permissions{}, nil
}

// getRemoteCommandExecutor returns an abstraction for a multiplexed bidirectional
// stream of a TTY on a given namespace and pod based on the given command.
func getRemoteCommandExecutor(
	ctx context.Context,
	config *rest.Config,
	user string,
	shell string,
	target *kubectlExecTarget,
) (remotecommand.Executor, error) {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	req := clientset.
		CoreV1().
		RESTClient().
		Post().
		Resource("pods").
		Name(target.pod).
		Namespace(target.namespace).
		SubResource("exec")

	option := &v1.PodExecOptions{
		Container: target.container,
		Command:   []string{shell},
		Stdin:     true,
		Stdout:    true,
		Stderr:    true,
		TTY:       true,
	}
	req = req.VersionedParams(option, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(config, http.MethodPost, req.URL())
	if err != nil {
		return nil, err
	}

	return exec, nil
}
