package sqlauthproxy

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/borderzero/border0-go/lib/types/pointer"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
	"go.uber.org/zap"
)

type mssqlHandler struct {
	Config
	UpstreamConfig msdsn.Config
	server         *mssql.Server
}

func newMssqlHandler(c Config) (*mssqlHandler, error) {
	config, err := msdsn.Parse(fmt.Sprintf("sqlserver://%s:%s@%s:%d", c.Username, c.Password, c.Hostname, c.Port))
	if err != nil {
		return nil, err
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
		Config:         c,
		UpstreamConfig: config,
		server:         server,
	}, nil
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

	// connect upstream
	upstreamConn, err := mssql.NewClient(ctx, h.UpstreamConfig)
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
