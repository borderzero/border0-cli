package serial

import (
	"context"
	"fmt"
	"io"

	"github.com/borderzero/border0-cli/internal/ssh_backends/common"
	"github.com/gliderlabs/ssh"
	"github.com/jacobsa/go-serial/serial"
	"go.uber.org/zap"
)

// GetSshServer returns an ssh server for which the handler drops
// clients into a given serial terminal with a given baud rate.
func GetSshServer(
	ctx context.Context,
	logger *zap.Logger,
	signerPrincipal string,
	signerPub []byte,
	device string,
	baudRate uint,
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
		Version:           "serial-device-proxy-ssh-server",
		HostSigners:       signers,
		Handler:           getSerialDeviceSshHandler(ctx, logger, device, baudRate),
		PublicKeyHandler:  publicKeyHandler,
		RequestHandlers:   ssh.DefaultRequestHandlers,
		ChannelHandlers:   ssh.DefaultChannelHandlers,
		SubsystemHandlers: ssh.DefaultSubsystemHandlers,
	}, nil
}

func getSerialDeviceSshHandler(
	ctx context.Context,
	logger *zap.Logger,
	device string,
	baudRate uint,
) ssh.Handler {
	return ssh.Handler(func(s ssh.Session) {
		options := serial.OpenOptions{
			PortName:        device, // e.g. "/dev/ttyS0"
			BaudRate:        baudRate,
			DataBits:        8,
			StopBits:        1,
			MinimumReadSize: 1,
		}
		port, err := serial.Open(options)
		if err != nil {
			logger.Error(
				"failed to open serial device",
				zap.String("device", device),
				zap.Uint("baud_rate", baudRate),
				zap.Error(err),
			)
			return
		}
		defer port.Close()
		defer s.Close()
		done := make(chan struct{})
		go func() { io.Copy(s, port); done <- struct{}{} }()
		go func() { io.Copy(port, s); done <- struct{}{} }()
		<-done
	})
}
