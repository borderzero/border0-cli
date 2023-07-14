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

func NewServer(logger *zap.Logger, ca string) (*ssh.Server, error) {
	handler := ssh.Handler(func(s ssh.Session) {
		user, err := user.Lookup(s.User())
		if err != nil {
			logger.Sugar().Errorf("could not find user: %s", err)
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

		logger.Sugar().Infof("new ssh session for %s (as user %s)", cert.KeyId, s.User())

		uid, _ := strconv.ParseUint(user.Uid, 10, 32)
		gid, _ := strconv.ParseUint(user.Gid, 10, 32)

		cmd.Env = []string{
			"LANG=en_US.UTF-8",
			"HOME=" + user.HomeDir,
			"USER=" + user.Username,
			"SHELL=" + shell,
		}

		cmd.Dir = user.HomeDir

		execCmd(s, cmd, uid, gid)
	})

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("could not generate rsa key: %s", err)
	}

	signer, err := gossh.NewSignerFromKey(key)
	if err != nil {
		return nil, fmt.Errorf("could not generate signer: %s", err)
	}

	requestHandlers := map[string]ssh.RequestHandler{}
	for k, v := range ssh.DefaultRequestHandlers {
		requestHandlers[k] = v
	}

	channelHandlers := map[string]ssh.ChannelHandler{}
	for k, v := range ssh.DefaultChannelHandlers {
		channelHandlers[k] = v
	}

	subsystemHandlers := map[string]ssh.SubsystemHandler{}
	for k, v := range ssh.DefaultSubsystemHandlers {
		subsystemHandlers[k] = v
	}

	return &ssh.Server{
		Version:     "Border0-ssh-server",
		HostSigners: []ssh.Signer{signer},
		Handler:     handler,
		PublicKeyHandler: func(ctx ssh.Context, key ssh.PublicKey) bool {
			pubCert, _, _, _, err := ssh.ParseAuthorizedKey([]byte(ca))
			if err != nil {
				logger.Error("error parsing public certificate", zap.Error(err))
				return false
			}

			cert, ok := key.(*gossh.Certificate)
			if !ok {
				logger.Error("error key is not a certificate")
				return false
			}

			if !bytes.Equal(cert.SignatureKey.Marshal(), pubCert.Marshal()) {
				// not logging error here because multiple public certs could be given to
				// ssh server, and some pub certs may not be valid
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
		RequestHandlers:   requestHandlers,
		ChannelHandlers:   channelHandlers,
		SubsystemHandlers: subsystemHandlers,
	}, nil
}

func getShell(user *user.User) (string, error) {
	switch runtime.GOOS {
	case "linux", "openbsd", "freebsd":
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
