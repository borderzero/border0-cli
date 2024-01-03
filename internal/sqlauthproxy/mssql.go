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
	"github.com/borderzero/border0-go/lib/types/pointer"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
	"go.uber.org/zap"
	"k8s.io/client-go/util/cert"
)

type mssqlHandler struct {
	Config
	UpstreamConfig  msdsn.Config
	server          *mssql.Server
	awsCredentials  aws.CredentialsProvider
	upstreamAddress string
}

func newMssqlHandler(c Config) (*mssqlHandler, error) {
	config, err := msdsn.Parse(fmt.Sprintf("sqlserver://%s:%s@%s:%d", c.Username, c.Password, c.Hostname, c.Port))
	if err != nil {
		return nil, err
	}

	upstreamAddress := net.JoinHostPort(c.Hostname, fmt.Sprintf("%d", c.Port))
	var awsCredentials aws.CredentialsProvider
	if c.RdsIam {
		cfg, err := util.GetAwsConfig(context.Background(), c.AwsRegion, c.AwsCredentials)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize AWS client: %v", err)
		}
		awsCredentials = cfg.Credentials
	}

	if c.UpstreamTLS {
		if len(c.UpstreamCABlock) > 0 {
			caPool, err := cert.NewPoolFromBytes(c.UpstreamCABlock)
			if err != nil {
				return nil, fmt.Errorf("failed to load upstream CA: %s", err)
			}
			config.TLSConfig.RootCAs = caPool
			config.TLSConfig.ServerName = c.Hostname
		} else if c.UpstreamCAFile != "" {
			caPool, err := cert.NewPool(c.UpstreamCAFile)
			if err != nil {
				return nil, fmt.Errorf("failed to load upstream CA: %s", err)
			}
			config.TLSConfig.RootCAs = caPool
			config.TLSConfig.ServerName = c.Hostname
		}

		if len(c.UpstreamCertBlock) > 0 && len(c.UpstreamKeyBlock) > 0 {
			cert, err := tls.X509KeyPair(c.UpstreamCertBlock, c.UpstreamKeyBlock)
			if err != nil {
				return nil, fmt.Errorf("failed to load upstream cert: %s", err)
			}
			config.TLSConfig.Certificates = []tls.Certificate{cert}
		} else if c.UpstreamCertFile != "" && c.UpstreamKeyFile != "" {
			cert, err := tls.LoadX509KeyPair(c.UpstreamCertFile, c.UpstreamKeyFile)
			if err != nil {
				return nil, fmt.Errorf("failed to load upstream cert: %s", err)
			}
			config.TLSConfig.Certificates = []tls.Certificate{cert}
		} else if c.UpstreamCertFile != "" || c.UpstreamKeyFile != "" {
			return nil, fmt.Errorf("upstream cert and key must both be provided")
		}

	}

	if !c.E2eEncryptionEnabled {
		return nil, fmt.Errorf("mssql proxy wihtout e2e encryption is not supported")
	}

	server, err := mssql.NewServer(mssql.ServerConfig{
		ProgName: pointer.To("Border0 MSSQL Proxy"),
		Version:  pointer.To("v16.0.4095"),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create mssql server: %w", err)
	}

	return &mssqlHandler{
		Config:          c,
		UpstreamConfig:  config,
		server:          server,
		awsCredentials:  awsCredentials,
		upstreamAddress: upstreamAddress,
	}, nil
}

type gcpDialer struct {
	DialContextFunc func(ctx context.Context, network, addr string) (net.Conn, error)
}

func (d gcpDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d.DialContextFunc(ctx, network, addr)
}

func (h mssqlHandler) handleClient(c net.Conn) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	defer c.Close()

	// handle login
	session, login, err := h.server.ReadLogin(c)
	if err != nil {
		h.Logger.Error("failed to handle login", zap.String("client", c.LocalAddr().String()), zap.Error(err))
		return
	}

	h.UpstreamConfig.Database = login.Database
	var tokenProvider func(ctx context.Context) (string, error)

	if h.RdsIam {
		tokenProvider = func(ctx context.Context) (string, error) {
			authToken, err := auth.BuildAuthToken(ctx, h.upstreamAddress, h.AwsRegion, h.Username, h.awsCredentials)
			if err != nil {
				h.Logger.Error("failed to create authentication token", zap.Error(err))
				return "", err

			}
			return authToken, nil
		}
	}

	var dialer mssql.Dialer
	if h.DialerFunc != nil {
		dialer = gcpDialer{DialContextFunc: h.DialerFunc}
	}

	// connect upstream
	upstreamConn, err := mssql.NewClient(ctx, h.UpstreamConfig, tokenProvider, dialer)
	if err != nil {
		h.Logger.Error("failed to connect upstream", zap.Error(err))
		return
	}

	defer upstreamConn.Close()

	if err := h.server.WriteLogin(session, upstreamConn.LoginEnvBytes()); err != nil {
		h.Logger.Error("failed to write login", zap.Error(err))
		return
	}

	e2EEncryptionConn, ok := c.(border0.E2EEncryptionConn)
	if !ok {
		c.Close()
		h.Logger.Error("failed to cast connection to e2eencryption")
		return
	}

	if e2EEncryptionConn.Metadata == nil {
		h.Logger.Error("invalid e2e metadata")
		return
	}

	serverHandler := &mssqlLocalHandler{
		logger:         h.Logger.With(zap.String("session_key", e2EEncryptionConn.Metadata.SessionKey)),
		metadata:       e2EEncryptionConn.Metadata,
		border0API:     h.Border0API,
		socket:         h.Socket,
		lastAuth:       time.Now(),
		recordingChan:  make(chan message, 100),
		upstreamConn:   upstreamConn,
		downstreamConn: session,
		database:       login.Database,
	}

	serverHandler.HandleConnection(ctx)
}
