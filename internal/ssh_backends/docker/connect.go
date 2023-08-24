package docker

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/borderzero/border0-cli/internal/ssh_backends/common"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/gliderlabs/ssh"
	"go.uber.org/zap"
)

// connect connects returns a net.Conn which under
// the hood is a TTY in a given docker container,
// leveraging Docker's "exec" command.
func connect(
	ctx context.Context,
	container string,
	user string,
	cmd []string,
) (net.Conn, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize new docker client: %v", err)
	}

	exec, err := cli.ContainerExecCreate(ctx, container, types.ExecConfig{
		User:       user, // User that will run the command
		Privileged: true, // Is the container in privileged mode
		Tty:        true,
		// ConsoleSize  *[2]uint `json:",omitempty"` // Initial console size [height, width]
		AttachStdin:  true,  // Attach the standard input, makes possible user interaction
		AttachStderr: true,  // Attach the standard error
		AttachStdout: true,  // Attach the standard output
		Detach:       false, // Execute in detach mode
		// DetachKeys   string   // Escape keys for detach
		// Env          []string // Environment variables
		// WorkingDir   string   // Working directory
		Cmd: cmd, // Execution commands and args
	})
	if err != nil {
		return nil, fmt.Errorf("failed to perform ContainerExecCreate operation against docker container \"%s\": %v", container, err)
	}

	hijackedResponse, err := cli.ContainerExecAttach(ctx, exec.ID, types.ExecStartCheck{
		Detach: false, // ExecStart will first check if it's detached
		Tty:    true,  // Check if there's a tty
		// ConsoleSize  *[2]uint `json:",omitempty"`// Terminal size [height, width], unused if Tty == false
	})
	if err != nil {
		return nil, fmt.Errorf("failed to perform ContainerExecAttach operation against docker container \"%s\": %v", container, err)
	}

	return hijackedResponse.Conn, nil
}

// GetSshServer returns an ssh server for which the handler
// drops the clients into a shell in a docker container.
func GetSshServer(
	ctx context.Context,
	logger *zap.Logger,
	signerPrincipal string,
	signerPub []byte,
	container string,
	user string,
	shell string,
) (*ssh.Server, error) {
	signers, err := common.GetSigners()
	if err != nil {
		return nil, fmt.Errorf("failed to get certificate signers: %v", err)
	}
	publicKeyHandler, err := common.GetPublicKeyHandler(ctx, logger, signerPrincipal, signerPub)
	if err != nil {
		return nil, fmt.Errorf("failed to get public key handler: %v", err)
	}
	return &ssh.Server{
		Version:           "docker-exec-proxy-ssh-server",
		HostSigners:       signers,
		Handler:           getDockerExecSshHandler(ctx, logger, container, user, shell),
		PublicKeyHandler:  publicKeyHandler,
		RequestHandlers:   ssh.DefaultRequestHandlers,
		ChannelHandlers:   ssh.DefaultChannelHandlers,
		SubsystemHandlers: ssh.DefaultSubsystemHandlers,
	}, nil
}

func getDockerExecSshHandler(
	ctx context.Context,
	logger *zap.Logger,
	container string,
	user string,
	shell string,
) ssh.Handler {
	return ssh.Handler(func(s ssh.Session) {
		conn, err := connect(ctx, container, user, []string{shell})
		if err != nil {
			logger.Error(
				"failed to connect to docker container",
				zap.String("container", container),
				zap.String("user", user),
				zap.String("shell", shell),
				zap.Error(err),
			)
			return
		}
		defer conn.Close()
		defer s.Close()
		done := make(chan struct{})
		go func() { io.Copy(s, conn); done <- struct{}{} }()
		go func() { io.Copy(conn, s); done <- struct{}{} }()
		<-done
	})
}
