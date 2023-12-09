package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/border0"
	sshConfig "github.com/borderzero/border0-cli/internal/ssh/config"
	"github.com/borderzero/border0-go/lib/types/set"
	"github.com/borderzero/border0-go/lib/types/slice"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/manifoldco/promptui"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

type dockerExecSessionHandler struct {
	logger      *zap.Logger
	proxyConfig *sshConfig.ProxyConfig
}

// ensure dockerExecSessionHandler implements SessionHandler.
var _ SessionHandler = (*dockerExecSessionHandler)(nil)

type dockerExecSession struct {
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

func NewDockerExecSessionHandler(
	logger *zap.Logger,
	proxyConfig *sshConfig.ProxyConfig,
) *dockerExecSessionHandler {
	return &dockerExecSessionHandler{
		logger:      logger,
		proxyConfig: proxyConfig,
	}
}

// Proxy runs the local proxying function between the connection to the
// remote Border0 proxy and the origin service (in this case the origin
// service is a connection to a remote docker executor / docker engine).
func (s *dockerExecSessionHandler) Proxy(conn net.Conn) {
	defer conn.Close()

	ctx := context.Background() // FIXME

	dockerSess := &dockerExecSession{
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
		dockerSess.e2eeMetadata = e2EEncryptionConn.Metadata
		dockerSess.logger = dockerSess.logger.With(zap.String("session_key", dockerSess.e2eeMetadata.SessionKey))
		// set the ssh server config's callback to the method on the dockerExecSession
		dockerSess.sshServerConfig.PublicKeyCallback = dockerSess.publicKeyCallback
	}

	// accept SSH connection from Border0 proxy
	dsConn, dsChanns, dsReqs, err := ssh.NewServerConn(conn, dockerSess.proxyConfig.SshServerConfig)
	if err != nil {
		dockerSess.logger.Error("failed to accept ssh connection from upstream proxy", zap.Error(err))
		return
	}
	dockerSess.downstreamSshConn = dsConn
	dockerSess.downstreamSshChans = dsChanns

	// we don't support global requests (yet)
	// so we can disregard the reqs channel
	go ssh.DiscardRequests(dsReqs)

	if dockerSess.proxyConfig.EndToEndEncryption {
		username := dockerSess.downstreamSshConn.User()

		if s.proxyConfig.Username != "" {
			username = s.proxyConfig.Username
		}

		if err := dockerSess.proxyConfig.Border0API.UpdateSession(models.SessionUpdate{
			SessionKey: dockerSess.e2eeMetadata.SessionKey,
			Socket:     dockerSess.proxyConfig.Socket,
			UserData:   ",sshuser=" + username,
		}); err != nil {
			dockerSess.logger.Error("failed to update session", zap.Error(err))
			return
		}
	}

	if err := dockerSess.handleChannels(ctx); err != nil {
		s.logger.Error("failed to handle channels", zap.Error(err))
		return
	}
}

func (s *dockerExecSession) handleChannels(ctx context.Context) error {
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
					if req.WantReply {
						req.Reply(true, nil)
					}
				// handled mostly for the benefit of session recordings
				case req.Type == "window-change":
					w, h := parseDims(req.Payload)
					s.sshWidth = int(w)
					s.sshHeight = int(h)
					if req.WantReply {
						req.Reply(true, nil)
					}
				case req.Type == "shell":
					if req.WantReply {
						req.Reply(true, nil)
					}
					go s.handleChannel(channelCtx, channel, s.downstreamSshConn.User())
				default:
					req.Reply(false, nil)
				}
			}
		}(requests)
	}

	return nil
}

func (s *dockerExecSession) handleChannel(
	ctx context.Context,
	channel ssh.Channel,
	user string,
) {
	defer channel.Close()

	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		channel.Write([]byte("An error occured. Try again later..."))
		s.logger.Error("failed to initialize new docker client", zap.Error(err))
		return
	}

	if s.proxyConfig.IsRecordingEnabled() {
		pwc := NewPipeWriteChannel(channel)
		channel = pwc
		r := NewRecording(s.logger, pwc.reader, s.proxyConfig.Socket.SocketID, s.e2eeMetadata.SessionKey, s.proxyConfig.Border0API, s.sshWidth, s.sshHeight)
		if err := r.Record(); err != nil {
			channel.Write([]byte("An error occured. Try again later..."))
			s.logger.Error("failed to record session", zap.Error(err))
			return
		}
		defer r.Stop()
	}

	container, err := s.askForTarget(ctx, channel)
	if err != nil {
		channel.Write([]byte("An error occured. Try again later..."))
		s.logger.Error("failed to determine target for docker exec", zap.Error(err))
		return
	}

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

		exec, err := cli.ContainerExecCreate(ctx, container, types.ExecConfig{
			User:       user, // User that will run the command
			Privileged: true, // Is the container in privileged mode
			Tty:        true,
			// ConsoleSize  *[2]uint `json:",omitempty"` // Initial console size [height, width]
			AttachStdin:  true,
			AttachStderr: true,
			AttachStdout: true,
			Detach:       false, // Execute in detach mode
			// DetachKeys   string   // Escape keys for detach
			// Env          []string // Environment variables
			// WorkingDir   string   // Working directory
			Cmd: []string{shell}, // Execution commands and args
		})
		if err != nil {
			channel.Write([]byte("An error occured. Try again later..."))
			s.logger.Error(
				"failed to perform ContainerExecCreate operation against docker container",
				zap.String("container", container),
				zap.Error(err),
			)
			return
		}

		hijackedResponse, err := cli.ContainerExecAttach(ctx, exec.ID, types.ExecStartCheck{
			Detach: false, // ExecStart will first check if it's detached
			Tty:    true,  // Check if there's a tty
			// ConsoleSize  *[2]uint `json:",omitempty"`// Terminal size [height, width], unused if Tty == false
		})
		if err != nil {
			channel.Write([]byte("An error occured. Try again later..."))
			s.logger.Error(
				"failed to perform ContainerExecAttach operation against docker container",
				zap.String("container", container),
				zap.Error(err),
			)
			return
		}
		defer hijackedResponse.Close()

		// perform one read to check whether the container
		// errored in finding shell in binaries path
		buf := make([]byte, 256)
		n, err := hijackedResponse.Conn.Read(buf)
		if err != nil {
			channel.Write([]byte("An error occured. Try again later..."))
			s.logger.Error(
				"failed to perform first-read on connection to docker remote executor",
				zap.String("container", container),
				zap.Error(err),
			)
		}
		if strings.Contains(string(buf[:n]), "executable file not found") ||
			strings.Contains(string(buf[:n]), "command terminated with exit code 127") {
			shellSet.Remove(shell)
			continue // try next shell
		}
		// if no error, we need to write that first read back to the ssh channel
		channel.Write(buf[:n])

		// stitch together connection to proxy and connection to executor
		errs := make(chan error, 2)
		var wg sync.WaitGroup // wait group is for channel closure

		go func() { wg.Wait(); close(errs) }()
		wg.Add(2)
		go func() { defer wg.Done(); _, e := io.Copy(channel, hijackedResponse.Conn); errs <- e }()
		go func() { defer wg.Done(); _, e := io.Copy(hijackedResponse.Conn, channel); errs <- e }()

		if err = <-errs; err != nil {
			if !errors.Is(err, io.EOF) {
				s.logger.Error(
					"error proxying between border0 proxy and docker remote executor",
					zap.String("container", container),
					zap.Error(err),
				)
			}
		}
		return
	}
}

func (s *dockerExecSession) askForTarget(ctx context.Context, channel ssh.Channel) (string, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "", fmt.Errorf("failed to initialize Docker client: %v", err)
	}

	containerListCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	containers, err := cli.ContainerList(containerListCtx, types.ContainerListOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to list Docker containers: %v", err)
	}

	if len(containers) == 0 {
		return "", fmt.Errorf("no containers available!")
	}

	ids := []string{}
	promptIdentifiers := []string{}
	for _, container := range containers {
		ids = append(ids, container.ID)

		friendlyName := container.ID
		if len(container.Names) > 0 {
			friendlyName = strings.Join(
				// strip leading slashes
				slice.Transform(
					container.Names,
					func(name string) string { return strings.TrimPrefix(name, "/") },
				),
				// use comma as join delimeter
				", ",
			)
		}

		promptIdentifiers = append(promptIdentifiers, fmt.Sprintf("%s (%s)", friendlyName, container.Image))
	}

	conatinerPrompt := promptui.Select{
		Label:             "Choose a container",
		Items:             promptIdentifiers,
		Stdout:            channel,
		Stdin:             channel,
		StartInSearchMode: true,
		Searcher: func(input string, index int) bool {
			return strings.Contains(strings.ToLower(promptIdentifiers[index]), strings.ToLower(input))
		},
	}
	index, _, err := conatinerPrompt.Run()
	if err != nil {
		return "", fmt.Errorf("unable to select container: %v", err)
	}

	return ids[index], nil
}

func (s *dockerExecSession) askForCommand(ctx context.Context, channel ssh.Channel) ([]string, error) {
	for {
		commandPrompt := promptui.Prompt{
			Label:  "What command would you like to run?",
			Stdout: channel,
			Stdin:  channel,
		}

		response, err := commandPrompt.Run()
		if err != nil {
			return nil, fmt.Errorf("unable to prompt for command: %v", err)
		}

		parts := strings.Split(response, " ")
		if len(parts) < 1 || parts[0] == "" {
			channel.Write([]byte("\r\nYou must provide a response, try again!\n"))
			continue
		}

		return parts, nil
	}
}

// FIXME: make generic, ssm uses the exact same code
func (s *dockerExecSession) publicKeyCallback(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
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
