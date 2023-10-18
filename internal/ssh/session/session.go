package session

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/borderzero/border0-cli/internal/ssh/config"
	"golang.org/x/crypto/ssh"
)

type SessionHandler interface {
	Proxy(conn net.Conn)
}

func newSSHServerConn(conn net.Conn, config *config.ProxyConfig) (sshConn *ssh.ServerConn, chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request, sessionKey *string, userEmail *string, err error) {
	sshConn, chans, reqs, err = ssh.NewServerConn(conn, config.SshServerConfig)
	if err != nil {
		err = fmt.Errorf("failed to accept ssh connection: %s", err)
		config.Logger.Error(err.Error())
		return
	}

	if config.EndToEndEncryption {
		e2eConn, ok := conn.(border0.E2EEncryptionConn)
		if !ok {
			err = errors.New("failed to cast connection to e2eencryption")
			config.Logger.Error(err.Error())
			return
		}

		if err := config.Border0API.UpdateSession(models.SessionUpdate{
			SessionKey: e2eConn.Metadata.SessionKey,
			Socket:     config.Socket,
			UserData:   ",sshuser=" + sshConn.User(),
		}); err != nil {
			err = fmt.Errorf("failed to update session: %s", err)
			config.Logger.Error(err.Error())
		}

		sessionKey = &e2eConn.Metadata.SessionKey
		userEmail = &e2eConn.Metadata.UserEmail
	}

	return
}

func parseDims(b []byte) (uint32, uint32) {
	w := binary.BigEndian.Uint32(b)
	h := binary.BigEndian.Uint32(b[4:])
	return w, h
}
