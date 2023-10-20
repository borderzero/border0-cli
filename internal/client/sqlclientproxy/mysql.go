package sqlclientproxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/borderzero/border0-cli/internal/client"
	mysqlClient "github.com/go-mysql-org/go-mysql/client"
	"go.uber.org/zap"

	"github.com/go-mysql-org/go-mysql/server"
)

type mysqlClientProxy struct {
	sqlClientProxy
	server *server.Server
}

type dummyProvider struct{}

var _ server.CredentialProvider = (*dummyProvider)(nil)

type mysqlServerHandler struct {
	server.EmptyHandler
	Database string
}

func (h *mysqlServerHandler) UseDB(dbName string) error {
	h.Database = dbName
	return nil
}

func (p *dummyProvider) CheckUsername(username string) (found bool, err error) {
	return true, nil
}

func (p *dummyProvider) GetCredential(username string) (password string, found bool, err error) {
	return "", true, nil
}

func newMysqlClientProxy(logger *zap.Logger, port int, resource models.ClientResource) (*mysqlClientProxy, error) {
	info, err := client.GetResourceInfo(logger, resource.Hostname())
	if err != nil {
		return nil, fmt.Errorf("failed to get resource info")
	}

	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{info.SetupTLSCertificate()},
		InsecureSkipVerify: true,
	}

	return &mysqlClientProxy{
		sqlClientProxy: sqlClientProxy{
			port:      port,
			info:      info,
			resource:  resource,
			tlsConfig: tlsConfig,
		},
		server: server.NewDefaultServer(),
	}, nil
}

func (p *mysqlClientProxy) Listen() error {
	l, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", p.port))
	if err != nil {
		return fmt.Errorf("failed to listen on port: %s", err)
	}

	defer l.Close()

	fmt.Printf("listening on %s\n", l.Addr().String())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	connCh := make(chan net.Conn)

	go func() {
		defer close(connCh)
		for {
			conn, err := l.Accept()
			if err != nil {
				if opErr, ok := err.(*net.OpError); ok && opErr.Err.Error() == "use of closed network connection" {
					return
				}

				fmt.Printf("failed to accept connection: %s\n", err)
				return
			}

			connCh <- conn
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case conn := <-connCh:
			go p.handleConnection(ctx, conn)
		}
	}
}

func (p *mysqlClientProxy) handleConnection(ctx context.Context, clientConn net.Conn) {
	defer clientConn.Close()

	serverHandler := &mysqlServerHandler{}
	proxyConn, err := server.NewCustomizedConn(clientConn, p.server, &dummyProvider{}, serverHandler)
	if err != nil {
		fmt.Println("failed to accept connection:", err)
		return
	}

	defer proxyConn.Close()

	serverConn, err := mysqlClient.ConnectWithDialer(ctx, "tcp", fmt.Sprintf("%s:%d", p.resource.Hostname(), p.info.Port), proxyConn.GetUser(), "", "", p.Dialer, func(c *mysqlClient.Conn) {
		c.SetTLSConfig(p.tlsConfig)
	})
	if err != nil {
		fmt.Println("failed to connect to socket:", err)
		return
	}

	if serverHandler.Database != "" {
		if err := serverConn.UseDB(serverHandler.Database); err != nil {
			fmt.Println("failed to use database:", err)
			return
		}
	}

	defer serverConn.Close()

	fmt.Printf("client %s connected to server\n", clientConn.RemoteAddr().String())
	border0.ProxyConnection(proxyConn.Conn.Conn, serverConn.Conn.Conn)
}

func (p *mysqlClientProxy) Dialer(ctx context.Context, network, addr string) (net.Conn, error) {
	if p.info.ConnectorAuthenticationEnabled || p.info.EndToEndEncryptionEnabled {
		return client.Connect(addr, p.tlsConfig, p.info.ConnectorAuthenticationEnabled, p.info.EndToEndEncryptionEnabled)
	} else {
		return net.DialTimeout("tcp", addr, 5*time.Second)
	}
}
