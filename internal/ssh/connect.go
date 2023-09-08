package ssh

import (
	"context"

	"github.com/borderzero/border0-cli/internal/api"
	"github.com/borderzero/border0-cli/internal/border0"
	"go.uber.org/zap"
)

type Connection struct {
	Socket *border0.Socket
	logger *zap.Logger
	api    api.API
}

func NewConnection(logger *zap.Logger, api api.API, socketID string, version string) (*Connection, error) {
	socket, err := border0.NewSocket(context.Background(), api, socketID, logger)
	if err != nil {
		return nil, err
	}

	socket.WithVersion(version)

	connection := &Connection{logger: logger, api: api, Socket: socket}

	return connection, nil
}

func (c *Connection) Close() {
	if c.Socket != nil {
		c.Socket.Close()
	}
}

func (c *Connection) IsClosed() bool {
	return c.Socket.IsClosed()
}
