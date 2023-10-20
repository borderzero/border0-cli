package session

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os/exec"
	"os/user"
	"strconv"
	"sync"
	"time"

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
	logger             *zap.Logger
	config             *config.ProxyConfig
	sessionKey         *string
	userEmail          *string
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

func NewLocalSession(logger *zap.Logger, config *config.ProxyConfig) *localSessionHandler {
	return &localSessionHandler{
		config: config,
		logger: logger,
	}
}

func (s *localSessionHandler) Proxy(conn net.Conn) {
	defer conn.Close()

	sshConn, chans, reqs, sessionKey, userEmail, err := newSSHServerConn(conn, s.config)
	if err != nil {
		s.logger.Error("failed to accept ssh connection", zap.Error(err))
		return
	}

	session := &localSession{
		config:             s.config,
		logger:             s.logger,
		downstreamSshConn:  sshConn,
		downstreamSshChans: chans,
		downstreamSshReqs:  reqs,
	}

	if s.config.Username != "" {
		session.username = s.config.Username
	} else {
		session.username = sshConn.User()
	}

	if sessionKey != nil {
		session.sessionKey = sessionKey
		session.logger = session.logger.With(zap.String("session_key", *sessionKey))
	}

	if userEmail != nil {
		session.userEmail = userEmail
	}

	// we don't support global requests (yet)
	// so we can disregard the reqs channel
	go ssh.DiscardRequests(reqs)
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

		r := NewRecording(c.logger, pr, *c.sessionKey, c.config.Border0API, c.window.Width, c.window.Height)
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

		r := NewRecording(c.logger, pwc.reader, *c.sessionKey, c.config.Border0API, c.window.Width, c.window.Height)
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

	c.logger.Info("new ssh session for", zap.String("user", *c.userEmail), zap.String("sshuser", c.username))

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
