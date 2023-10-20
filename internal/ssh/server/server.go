package server

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/gliderlabs/ssh"
	"go.uber.org/zap"
	gossh "golang.org/x/crypto/ssh"
)

type options struct {
	username string
	shell    string
}

// Option represents a configuration option for the ssh server.
type Option func(*options)

// WithUsername is the option to override the ssh username.
func WithUsername(username string) Option { return func(o *options) { o.username = username } }

// WithShell is the option to override the ssh shell.
func WithShell(shell string) Option { return func(o *options) { o.shell = shell } }

// NewServer returns a new ssh server.
func NewServer(logger *zap.Logger, ca string, opts ...Option) (*ssh.Server, error) {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	handler := ssh.Handler(func(s ssh.Session) {
		var shell string

		username := s.User()
		if o.username != "" {
			username = o.username
		}

		user, err := user.Lookup(username)
		if err != nil {
			logger.Sugar().Errorf("could not find user \"%s\": %v", username, err)
			return
		}
		if o.shell != "" {
			shell = o.shell
		} else {
			shell, err = GetShell(user)
			if err != nil {
				logger.Sugar().Errorf("could not get user shell: %s", err)
				return
			}
		}

		var cmd exec.Cmd

		cmd.Path = shell
		cmd.Args = []string{shell}

		pubKey := s.PublicKey()
		cert, ok := pubKey.(*gossh.Certificate)
		if !ok {
			logger.Sugar().Errorf("could not get user certificate")
			return
		}

		logger.Sugar().Infof("new ssh session for %s (as user %s)", cert.KeyId, username)

		uid, _ := strconv.ParseUint(user.Uid, 10, 32)
		gid, _ := strconv.ParseUint(user.Gid, 10, 32)

		cmd.Env = []string{
			"LANG=en_US.UTF-8",
			"HOME=" + user.HomeDir,
			"USER=" + user.Username,
			"SHELL=" + shell,
		}
		cmd.Dir = user.HomeDir

		execCmd(s, cmd, uid, gid, username)
	})

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("could not generate rsa key: %s", err)
	}

	signer, err := gossh.NewSignerFromKey(key)
	if err != nil {
		return nil, fmt.Errorf("could not generate signer: %s", err)
	}

	return &ssh.Server{
		Version:     "Border0-ssh-server",
		HostSigners: []ssh.Signer{signer},
		Handler:     handler,
		PublicKeyHandler: func(ctx ssh.Context, key ssh.PublicKey) bool {
			pubCert, _, _, _, err := ssh.ParseAuthorizedKey([]byte(ca))
			if err != nil {
				logger.Error("error parsing public key", zap.Error(err))
				return false
			}

			cert, ok := key.(*gossh.Certificate)
			if !ok {
				return false
			}

			if !bytes.Equal(cert.SignatureKey.Marshal(), pubCert.Marshal()) {
				return false
			}

			var certChecker gossh.CertChecker

			err = certChecker.CheckCert("mysocket_ssh_signed", cert)
			if err != nil {
				logger.Error("error validating certificate", zap.Error(err))
				return false
			}

			return true
		},
		SubsystemHandlers: map[string]ssh.SubsystemHandler{
			"sftp": func(s ssh.Session) {
				pubKey := s.PublicKey()
				cert, ok := pubKey.(*gossh.Certificate)
				if !ok {
					logger.Sugar().Errorf("could not get user certificate")
					return
				}

				logger.Sugar().Infof("new sftp session for %s (as user %s)", cert.KeyId, s.User())
				username := s.User()
				if o.username != "" {
					username = o.username
				}

				if err := StartChildProcess(s.Context(), s, "sftp", username); err != nil {
					logger.Error("error starting sftp child process", zap.Error(err))
				}
			},
		},
	}, nil
}

func GetShell(user *user.User) (string, error) {
	switch runtime.GOOS {
	case "linux", "openbsd", "freebsd":
		if _, err := exec.LookPath("getent"); err != nil {
			return "/bin/sh", nil
		}

		out, err := exec.Command("getent", "passwd", user.Uid).Output()
		if err != nil {
			return "", err
		}

		ent := strings.Split(strings.TrimSuffix(string(out), "\n"), ":")
		return ent[6], nil
	case "darwin":
		dir := "Local/Default/Users/" + user.Username
		out, err := exec.Command("dscl", "localhost", "-read", dir, "UserShell").Output()
		if err != nil {
			return "", err
		}

		re := regexp.MustCompile("UserShell: (/[^ ]+)\n")
		matched := re.FindStringSubmatch(string(out))
		shell := matched[1]
		if shell == "" {
			return "", fmt.Errorf("invalid output: %s", string(out))
		}

		return shell, nil
	case "windows":
		consoleApp := os.Getenv("COMSPEC")
		if consoleApp == "" {
			consoleApp = "cmd.exe"
		}

		return consoleApp, nil
	}

	return "", errors.New("unsupported platform")
}

func execCmd(s ssh.Session, cmd exec.Cmd, uid, gid uint64, username string) {
	pty, winCh, isPty := s.Pty()
	exitCode := ExecCmd(s, s.RawCommand(), pty.Term, isPty, winCh, cmd, uid, gid, username)
	s.Exit(exitCode)
}
