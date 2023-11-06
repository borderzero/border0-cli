package session

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"os/user"
	"strconv"
	"sync"
	"time"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/borderzero/border0-cli/internal/ssh/config"
	"github.com/borderzero/border0-cli/internal/ssh/server"
	gliderlabs_ssh "github.com/gliderlabs/ssh"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

type localSessionHandler struct {
	logger *zap.Logger
	config *config.ProxyConfig
}

type localSession struct {
	config             *config.ProxyConfig
	metadata           *border0.E2EEncryptionMetadata
	sshServerConfig    *ssh.ServerConfig
	logger             *zap.Logger
	username           string
	downstreamSshConn  *ssh.ServerConn
	downstreamSshChans <-chan ssh.NewChannel
	downstreamSshReqs  <-chan *ssh.Request
}

type localChannel struct {
	*localSession
	downstreamChannel ssh.Channel
	window            gliderlabs_ssh.Window
	env               []string
	pty               bool
	ptyTerm           string
	winch             chan gliderlabs_ssh.Window
}

func NewLocalSessionHandler(logger *zap.Logger, config *config.ProxyConfig) *localSessionHandler {
	return &localSessionHandler{
		config: config,
		logger: logger,
	}
}

func (s *localSessionHandler) Proxy(conn net.Conn) {
	defer conn.Close()

	session := &localSession{
		logger:          s.logger,
		config:          s.config,
		sshServerConfig: s.config.SshServerConfig,
	}

	if s.config.EndToEndEncryption {
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

		session.metadata = e2EEncryptionConn.Metadata
		session.sshServerConfig.PublicKeyCallback = session.publicKeyCallback
		session.logger = session.logger.With(zap.String("session_key", session.metadata.SessionKey))
	}

	var err error
	session.downstreamSshConn, session.downstreamSshChans, session.downstreamSshReqs, err = ssh.NewServerConn(conn, session.config.SshServerConfig)
	if err != nil {
		session.logger.Error("failed to accept ssh connection", zap.Error(err))
		return
	}

	if s.config.Username != "" {
		session.username = s.config.Username
	} else {
		session.username = session.downstreamSshConn.User()
	}

	if session.config.EndToEndEncryption {
		if err := session.config.Border0API.UpdateSession(models.SessionUpdate{
			SessionKey: session.metadata.SessionKey,
			Socket:     session.config.Socket,
			UserData:   ",sshuser=" + session.username,
		}); err != nil {
			session.logger.Error("failed to update session", zap.Error(err))
			return
		}
	}

	// we don't support global requests (yet)
	// so we can disregard the reqs channel
	go ssh.DiscardRequests(session.downstreamSshReqs)
	if err := session.handleChannels(); err != nil {
		s.logger.Error("failed to handle channels", zap.Error(err))
		return
	}
}

func (s *localSession) handleChannels() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer s.downstreamSshConn.Close()

	for {
		select {
		case newChannel := <-s.downstreamSshChans:
			if newChannel == nil {
				return nil
			}

			switch newChannel.ChannelType() {
			case "session":
				go s.handleSessionChannel(ctx, newChannel)
			case "direct-tcpip":
				go s.handleDirectTcpipChannel(ctx, newChannel)
			default:
				s.logger.Error("unknown client channel type", zap.String("channel_type", newChannel.ChannelType()))
				if err := newChannel.Reject(ssh.UnknownChannelType, fmt.Sprintf("unknown channel type: %s", newChannel.ChannelType())); err != nil {
					return err
				}
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func (s *localSession) handleDirectTcpipChannel(ctx context.Context, newChannel ssh.NewChannel) {
	var localForwardChannel struct {
		DestAddr   string
		DestPort   uint32
		OriginAddr string
		OriginPort uint32
	}

	if err := ssh.Unmarshal(newChannel.ExtraData(), &localForwardChannel); err != nil {
		s.logger.Error("failed to unmarshal payload", zap.Error(err))
		newChannel.Reject(ssh.ConnectionFailed, "failed to parse data")
		return
	}

	var dialer net.Dialer
	dialer.Timeout = time.Second * 5
	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", localForwardChannel.DestAddr, localForwardChannel.DestPort))
	if err != nil {
		newChannel.Reject(ssh.ConnectionFailed, err.Error())
		return
	}

	sshChannel, reqs, err := newChannel.Accept()
	if err != nil {
		conn.Close()
		s.logger.Error("failed to accept channel", zap.Error(err))
		return
	}

	// in a direct-tcpip channel incomming requests are discarded
	// we only care about the data
	go ssh.DiscardRequests(reqs)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		defer sshChannel.Close()
		defer conn.Close()

		io.Copy(sshChannel, conn)
	}()

	go func() {
		defer wg.Done()
		defer sshChannel.Close()
		defer conn.Close()

		io.Copy(conn, sshChannel)
	}()

	wg.Wait()
}

func (s *localSession) handleSessionChannel(ctx context.Context, newChannel ssh.NewChannel) {
	channel := &localChannel{
		localSession: s,
	}

	downstreamChannel, downstreamChannelRequests, err := newChannel.Accept()
	if err != nil {
		s.logger.Error("failed to accept channel", zap.Error(err))
		return
	}

	channel.downstreamChannel = downstreamChannel
	defer downstreamChannel.Close()

	for {
		select {
		case <-ctx.Done():
			return
		case req := <-downstreamChannelRequests:
			if req == nil {
				return
			}

			channel.handleRequest(req)
		}
	}
}

func (s *localChannel) handleRequest(req *ssh.Request) {
	switch req.Type {
	case "env":
		var env struct{ Key, Value string }
		ssh.Unmarshal(req.Payload, &env)
		s.env = append(s.env, fmt.Sprintf("%s=%s", env.Key, env.Value))
		req.Reply(true, nil)
	case "pty-req":
		if s.pty {
			req.Reply(false, nil)
			return
		}

		if len(req.Payload) < 4 {
			req.Reply(false, nil)
			return
		}

		length := binary.BigEndian.Uint32(req.Payload)
		if uint32(len(req.Payload)) < 4+length {
			req.Reply(false, nil)
			return
		}

		s.ptyTerm = string(req.Payload[4 : 4+length])
		w, h := parseDims(req.Payload[length+4:])
		s.window.Width = int(w)
		s.window.Height = int(h)
		s.winch = make(chan gliderlabs_ssh.Window, 1)
		s.winch <- s.window
		s.pty = true
		req.Reply(true, nil)
	case "window-change":
		if !s.pty {
			req.Reply(false, nil)
			return
		}

		w, h := parseDims(req.Payload)
		s.window.Width = int(w)
		s.window.Height = int(h)
		s.winch <- s.window
		if req.WantReply {
			req.Reply(true, nil)
		}
	case "subsystem":
		if string(req.Payload[4:]) == "sftp" {
			go s.handleSftp(req)
		} else {
			s.logger.Error("unknown subsystem", zap.String("subsystem", string(req.Payload[4:])))
			req.Reply(false, nil)
		}
	case "exec", "shell":
		go s.handleExec(req)
	default:
		s.logger.Error("unknown request type", zap.String("request_type", req.Type))
		req.Reply(false, nil)
	}
}

func (c *localChannel) handleSftp(req *ssh.Request) {
	defer c.downstreamChannel.Close()

	if req.WantReply {
		if err := req.Reply(true, nil); err != nil {
			c.logger.Error("failed to reply to request", zap.Error(err))
			return
		}
	}

	if c.config.IsRecordingEnabled() {
		pr, pw := io.Pipe()

		r := NewRecording(c.logger, pr, c.config.Socket.SocketID, c.metadata.SessionKey, c.config.Border0API, c.window.Width, c.window.Height)
		if err := r.Record(); err != nil {
			c.logger.Error("failed to record session", zap.Error(err))
			return
		}

		pw.Write([]byte(fmt.Sprintf("starting a %s session\n", string(req.Payload[4:]))))
		defer r.Stop()
	}

	ctx := context.Background()
	err := server.StartChildProcess(ctx, c.downstreamChannel, "sftp", c.username)
	if err != nil {
		c.logger.Error("error starting sftp child process", zap.Error(err))
	}

	closeChannel(c.downstreamChannel, err)
}

func (c *localChannel) handleExec(req *ssh.Request) {
	defer c.downstreamChannel.Close()

	if req.WantReply {
		if err := req.Reply(true, nil); err != nil {
			c.logger.Error("failed to reply to request", zap.Error(err))
			return
		}
	}

	var payload = struct{ Value string }{}
	ssh.Unmarshal(req.Payload, &payload)
	command := payload.Value

	if c.config.IsRecordingEnabled() {
		pwc := NewPipeWriteChannel(c.downstreamChannel)
		c.downstreamChannel = pwc

		r := NewRecording(c.logger, pwc.reader, c.config.Socket.SocketID, c.metadata.SessionKey, c.config.Border0API, c.window.Width, c.window.Height)
		if err := r.Record(); err != nil {
			c.logger.Error("failed to record session", zap.Error(err))
			return
		}

		if command != "" {
			pwc.logWriter.Write([]byte(fmt.Sprintf("%s\n", command)))
		}

		defer r.Stop()
	}

	user, err := user.Lookup(c.username)
	if err != nil {
		c.logger.Error("could not find user", zap.String("username", c.username), zap.Error(err))
		return
	}

	shell, err := server.GetShell(user)
	if err != nil {
		c.logger.Error("could not get user shell", zap.Error(err))
		return
	}

	var cmd exec.Cmd
	cmd.Path = shell
	cmd.Args = []string{shell}

	c.logger.Info("new ssh session for", zap.String("user", c.metadata.UserEmail), zap.String("sshuser", c.username))

	uid, _ := strconv.ParseUint(user.Uid, 10, 32)
	gid, _ := strconv.ParseUint(user.Gid, 10, 32)

	cmd.Env = []string{
		"LANG=en_US.UTF-8",
		"HOME=" + user.HomeDir,
		"USER=" + user.Username,
		"SHELL=" + shell,
	}

	cmd.Env = append(cmd.Env, c.env...)
	cmd.Dir = user.HomeDir

	defer func() {
		if c.winch != nil {
			close(c.winch)
		}
	}()

	exitStatus := server.ExecCmd(c.downstreamChannel, command, c.ptyTerm, c.pty, c.winch, cmd, uid, gid, c.username)
	status := struct{ Status uint32 }{Status: uint32(exitStatus)}
	c.downstreamChannel.SendRequest("exit-status", false, ssh.Marshal(&status))
}

func (s *localSession) publicKeyCallback(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	cert, ok := key.(*ssh.Certificate)
	if !ok {
		return nil, errors.New("can not cast certificate")
	}

	if s.config.OrgSshCA == nil {
		return nil, errors.New("error: unable to validate certificate, no CA configured")
	}

	if bytes.Equal(cert.SignatureKey.Marshal(), s.config.OrgSshCA.Marshal()) {
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

	actions, _, err := s.config.Border0API.Evaluate(ctx, s.config.Socket, s.metadata.ClientIP, s.metadata.UserEmail, s.metadata.SessionKey)
	if err != nil {
		return nil, fmt.Errorf("error: failed to authorize: %s", err)
	}

	if len(actions) == 0 {
		return nil, errors.New("error: authorization failed")
	}

	return &ssh.Permissions{}, nil
}
