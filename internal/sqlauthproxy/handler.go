package sqlauthproxy

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/borderzero/border0-cli/internal/cloudsql"
	"github.com/borderzero/border0-go/types/common"
	"go.uber.org/zap"
)

const (
	authTTL = 1 * time.Minute
)

type handler interface {
	handleClient(c net.Conn)
}

type Config struct {
	Logger               *zap.Logger
	Hostname             string
	Port                 int
	RdsIam               bool
	Username             string
	Password             string
	UpstreamType         string
	UpstreamCAFile       string
	UpstreamCertFile     string
	UpstreamKeyFile      string
	UpstreamCABlock      []byte
	UpstreamCertBlock    []byte
	UpstreamKeyBlock     []byte
	UpstreamTLS          bool
	AwsRegion            string
	AwsCredentials       *common.AwsCredentials
	DialerFunc           func(context.Context, string, string) (net.Conn, error)
	E2eEncryptionEnabled bool
	Socket               models.Socket
	Border0API           border0.Border0API
}

func Serve(l net.Listener, config Config) error {
	var handler handler
	var err error

	switch config.UpstreamType {
	case "postgres":
		handler, err = newPostgresHandler(config)
		if err != nil {
			return err
		}
	default:
		handler, err = newMysqlHandler(config)
		if err != nil {
			return err
		}
	}

	for {
		rconn, err := l.Accept()
		if err != nil {
			config.Logger.Error("failed to accept connection", zap.Error(err))
			continue
		}

		go handler.handleClient(rconn)
	}
}

func BuildHandlerConfig(logger *zap.Logger, socket models.Socket, border0API border0.Border0API) (*Config, error) {
	upstreamTLS := true
	if socket.ConnectorLocalData.UpstreamTLS != nil {
		upstreamTLS = *socket.ConnectorLocalData.UpstreamTLS
	}

	handlerConfig := &Config{
		Logger:               logger,
		Hostname:             socket.ConnectorData.TargetHostname,
		Port:                 socket.ConnectorData.Port,
		RdsIam:               socket.ConnectorLocalData.RdsIAMAuth,
		Username:             socket.ConnectorLocalData.UpstreamUsername,
		Password:             socket.ConnectorLocalData.UpstreamPassword,
		UpstreamType:         socket.UpstreamType,
		AwsRegion:            socket.ConnectorLocalData.AWSRegion,
		AwsCredentials:       socket.ConnectorLocalData.AwsCredentials,
		UpstreamCAFile:       socket.ConnectorLocalData.UpstreamCACertFile,
		UpstreamCertFile:     socket.ConnectorLocalData.UpstreamCertFile,
		UpstreamKeyFile:      socket.ConnectorLocalData.UpstreamKeyFile,
		UpstreamCABlock:      socket.ConnectorLocalData.UpstreamCACertBlock,
		UpstreamCertBlock:    socket.ConnectorLocalData.UpstreamCertBlock,
		UpstreamKeyBlock:     socket.ConnectorLocalData.UpstreamKeyBlock,
		UpstreamTLS:          upstreamTLS,
		E2eEncryptionEnabled: socket.EndToEndEncryptionEnabled,
		Socket:               socket,
		Border0API:           border0API,
	}

	if socket.ConnectorLocalData.CloudSQLConnector {
		if socket.ConnectorLocalData.CloudSQLInstance == "" {
			return nil, fmt.Errorf("cloudsql instance is not defined")
		}

		ctx := context.Background()
		dialer, err := cloudsql.NewDialer(
			ctx,
			socket.ConnectorLocalData.CloudSQLInstance,
			socket.ConnectorLocalData.GoogleCredentialsFile,
			socket.ConnectorLocalData.GoogleCredentialsJSON,
			socket.ConnectorLocalData.CloudSQLIAMAuth,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create dialer for cloudSQL: %s", err)
		}

		handlerConfig.DialerFunc = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.Dial(ctx, socket.ConnectorLocalData.CloudSQLInstance)
		}
	}

	return handlerConfig, nil
}
