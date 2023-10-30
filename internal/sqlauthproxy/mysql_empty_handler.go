package sqlauthproxy

import (
	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/go-mysql-org/go-mysql/client"
	"github.com/go-mysql-org/go-mysql/server"
	"go.uber.org/zap"
)

type mysqlEmptyHandler struct {
	logger *zap.Logger
	server.EmptyHandler
	database   string
	clientConn *client.Conn
}

var _ server.Handler = &mysqlEmptyHandler{}

func (h *mysqlEmptyHandler) UseDB(dbName string) error {
	h.database = dbName
	return h.clientConn.UseDB(dbName)
}

func (h *mysqlEmptyHandler) Database() string {
	return h.database
}

func (h *mysqlEmptyHandler) HandleConnection(serverConn *server.Conn, clientConn *client.Conn) {
	border0.ProxyConnection(serverConn.Conn.Conn, clientConn.Conn.Conn)
}
