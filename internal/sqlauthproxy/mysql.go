package sqlauthproxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/borderzero/border0-cli/internal/util"
	"github.com/go-mysql-org/go-mysql/client"
	"github.com/go-mysql-org/go-mysql/server"
	"go.uber.org/zap"
	"k8s.io/client-go/util/cert"
)

type mysqlHandler struct {
	Config
	options         []func(*client.Conn)
	awsCredentials  aws.CredentialsProvider
	server          *server.Server
	upstreamAddress string
}

type dummyProvider struct{}

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

func newMysqlHandler(c Config) (*mysqlHandler, error) {
	upstreamAddress := net.JoinHostPort(c.Hostname, fmt.Sprintf("%d", c.Port))
	var options []func(*client.Conn)
	var awsCredentials aws.CredentialsProvider
	if c.RdsIam {
		cfg, err := util.GetAwsConfig(context.Background(), c.AwsRegion, c.AwsCredentials)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize AWS client: %v", err)
		}
		awsCredentials = cfg.Credentials
	}

	if c.UpstreamTLS && c.DialerFunc == nil {
		tlsConfig := &tls.Config{}

		if len(c.UpstreamCABlock) > 0 {
			caPool, err := cert.NewPoolFromBytes(c.UpstreamCABlock)
			if err != nil {
				return nil, fmt.Errorf("failed to load upstream CA: %s", err)
			}
			tlsConfig.RootCAs = caPool
			tlsConfig.ServerName = c.Hostname
		} else if c.UpstreamCAFile != "" {
			caPool, err := cert.NewPool(c.UpstreamCAFile)
			if err != nil {
				return nil, fmt.Errorf("failed to load upstream CA: %s", err)
			}
			tlsConfig.RootCAs = caPool
			tlsConfig.ServerName = c.Hostname
		} else {
			tlsConfig.InsecureSkipVerify = true
		}

		if len(c.UpstreamCertBlock) > 0 && len(c.UpstreamKeyBlock) > 0 {
			cert, err := tls.X509KeyPair(c.UpstreamCertBlock, c.UpstreamKeyBlock)
			if err != nil {
				return nil, fmt.Errorf("failed to load upstream cert: %s", err)
			}
			tlsConfig.Certificates = []tls.Certificate{cert}
		} else if c.UpstreamCertFile != "" && c.UpstreamKeyFile != "" {
			cert, err := tls.LoadX509KeyPair(c.UpstreamCertFile, c.UpstreamKeyFile)
			if err != nil {
				return nil, fmt.Errorf("failed to load upstream cert: %s", err)
			}
			tlsConfig.Certificates = []tls.Certificate{cert}
		} else if c.UpstreamCertFile != "" || c.UpstreamKeyFile != "" {
			return nil, fmt.Errorf("upstream cert and key must both be provided")
		}

		options = append(options, func(c *client.Conn) { c.SetTLSConfig(tlsConfig) })
	}

	mysqlHandler := &mysqlHandler{
		Config:          c,
		server:          server.NewDefaultServer(),
		upstreamAddress: upstreamAddress,
		awsCredentials:  awsCredentials,
		options:         options,
	}

	return mysqlHandler, nil
}

func (h mysqlHandler) handleClient(c net.Conn) {
	defer c.Close()

	serverHandler := &mysqlServerHandler{}
	clientConn, err := server.NewCustomizedConn(c, h.server, &dummyProvider{}, serverHandler)
	if err != nil {
		h.Logger.Error("failed to accept connection", zap.Error(err))
		return
	}

	if h.RdsIam {
		authenticationToken, err := auth.BuildAuthToken(context.TODO(), h.upstreamAddress, h.AwsRegion, h.Username, h.awsCredentials)
		if err != nil {
			h.Logger.Error("failed to create authentication token", zap.Error(err))
			return
		}

		h.Password = authenticationToken
	}

	if h.DialerFunc == nil {
		h.DialerFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.DialTimeout(network, addr, 5*time.Second)
		}
	}

	serverConn, err := client.ConnectWithDialer(context.Background(), "tcp", h.upstreamAddress, h.Username, h.Password, serverHandler.Database, h.DialerFunc, h.options...)
	if err != nil {
		h.Logger.Error("sqlauthproxy: failed to connect upstream:", zap.Error(err))
		return
	}

	border0.ProxyConnection(clientConn.Conn.Conn, serverConn.Conn.Conn)
}
