package ssh

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
}

// Option represents a configuration option for the ssh server.
type Option func(*options)

// WithUsername is the option to override the ssh username.
func WithUsername(username string) Option { return func(o *options) { o.username = username } }

// NewServer returns a new ssh server.
func NewServer(logger *zap.Logger, ca string, opts ...Option) (*ssh.Server, error) {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	handler := ssh.Handler(func(s ssh.Session) {
		username := s.User()
		if o.username != "" {
			username = o.username
		}

		user, err := user.Lookup(username)
		if err != nil {
			logger.Sugar().Errorf("could not find user \"%s\": %v", username, err)
			return
		}

		shell, err := getShell(user)
		if err != nil {
			logger.Sugar().Errorf("could not get user shell: %s", err)
			return
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

				if err := startChildProcess(s, "sftp", username); err != nil {
					logger.Sugar().Errorf("could not start sftp child process: %s", err)
				}

			},
		},
	}, nil
}

func startChildProcess(s ssh.Session, process, username string) error {
	user, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("could not find user %s: %v", username, err)
	}

	uidv, err := strconv.ParseInt(user.Uid, 10, 32)
	if err != nil {
		return fmt.Errorf("could not parse uid: %v", err)
	}
	uid := int(uidv)

	euid := os.Geteuid()
	if uid != euid && euid != 0 {
		return fmt.Errorf("need root privileges to start child process as another user")
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not get executable path: %s", err)
	}

	groups, err := user.GroupIds()
	if err != nil {
		return fmt.Errorf("could not get user groups: %s", err)
	}

	commandArgs := []string{"child", process, "--user", user.Name, "--uid", user.Uid, "--gid", user.Gid}
	if len(groups) > 0 {
		for _, group := range groups {
			commandArgs = append(commandArgs, "--group", group)
		}
	}

	cmd := exec.CommandContext(s.Context(), executable, commandArgs...)
	cmd.Stdin = s
	cmd.Stdout = s
	cmd.Dir = user.HomeDir

	return cmd.Run()
}

func getShell(user *user.User) (string, error) {
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
