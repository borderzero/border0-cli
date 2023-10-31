package sqlauthproxy

import (
	"net"

	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/jackc/pgconn"
)

type postgresCopyyHandler struct {
	clientConn *pgconn.HijackedConn
	serverConn net.Conn
}

func (h *postgresCopyyHandler) HandleConnection() {
	border0.ProxyConnection(h.serverConn, h.clientConn.Conn)
}
