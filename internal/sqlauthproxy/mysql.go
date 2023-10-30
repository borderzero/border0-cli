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
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/server"
	"go.uber.org/zap"
	"k8s.io/client-go/util/cert"

	pe "github.com/pingcap/errors"
)

const (
	serverVersion = "5.7.0"
)

type mysqlServerHandler interface {
	server.Handler
	Database() string
	HandleConnection(serverConn *server.Conn, clientConn *client.Conn)
}

type mysqlHandler struct {
	Config
	logger          *zap.Logger
	options         []func(*client.Conn)
	awsCredentials  aws.CredentialsProvider
	server          *server.Server
	upstreamAddress string
}

type dummyProvider struct{}

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

	var mysqlServer *server.Server
	if c.e2eEncryptionEnabled {
		mysqlServer = server.NewServer(serverVersion, mysql.DEFAULT_COLLATION_ID, mysql.AUTH_NATIVE_PASSWORD, nil, nil)
	} else {
		mysqlServer = server.NewDefaultServer()
	}

	mysqlHandler := &mysqlHandler{
		Config:          c,
		logger:          c.Logger,
		server:          mysqlServer,
		upstreamAddress: upstreamAddress,
		awsCredentials:  awsCredentials,
		options:         options,
	}

	return mysqlHandler, nil
}

func (h mysqlHandler) handleClient(c net.Conn) {
	defer c.Close()

	password := h.Password
	if h.RdsIam {
		authenticationToken, err := auth.BuildAuthToken(context.TODO(), h.upstreamAddress, h.AwsRegion, h.Username, h.awsCredentials)
		if err != nil {
			h.logger.Error("failed to create authentication token", zap.Error(err))
			return
		}

		password = authenticationToken
	}

	if h.DialerFunc == nil {
		h.DialerFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.DialTimeout(network, addr, 5*time.Second)
		}
	}

	clientConn, err := client.ConnectWithDialer(context.Background(), "tcp", h.upstreamAddress, h.Username, password, "", h.DialerFunc, h.options...)
	if err != nil {
		h.logger.Error("upstream mysql connection failed:", zap.Error(pe.Unwrap(err)))
		return
	}

	defer func() {
		if clientConn != nil {
			clientConn.Close()
		}
	}()

	var serverHandler mysqlServerHandler
	if h.Config.e2eEncryptionEnabled {
		e2EEncryptionConn, ok := c.(border0.E2EEncryptionConn)
		if !ok {
			c.Close()
			h.logger.Error("failed to cast connection to e2eencryption")
			return
		}

		if e2EEncryptionConn.Metadata == nil {
			h.logger.Error("invalid e2e metadata")
			return
		}

		serverHandler = &mysqlLocalHandler{
			logger:          h.logger.With(zap.String("session_key", e2EEncryptionConn.Metadata.SessionKey)),
			metadata:        e2EEncryptionConn.Metadata,
			statements:      make(map[int64]*client.Stmt),
			preparedQueries: make(map[int64]string),
			border0API:      h.border0API,
			socket:          h.socket,
			lastAuth:        time.Now(),
			recordingChan:   make(chan message, 100),
			clientConn:      clientConn,
		}
	} else {
		serverHandler = &mysqlEmptyHandler{
			logger:     h.logger,
			clientConn: clientConn,
		}
	}

	serverConn, err := server.NewCustomizedConn(c, h.server, &dummyProvider{}, serverHandler)
	if err != nil {
		h.Logger.Error("failed to accept connection", zap.Error(pe.Unwrap(err)))
		return
	}

	defer func() {
		if serverConn != nil && !serverConn.Closed() {
			serverConn.Close()
		}
	}()

	serverHandler.HandleConnection(serverConn, clientConn)
}
