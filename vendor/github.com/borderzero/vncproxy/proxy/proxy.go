package proxy

import (
	"context"
	"fmt"
	"net"
	"path"
	"strconv"
	"time"

	"github.com/borderzero/vncproxy/client"
	"github.com/borderzero/vncproxy/common"
	"github.com/borderzero/vncproxy/encodings"
	listeners "github.com/borderzero/vncproxy/recorder"
	"github.com/borderzero/vncproxy/server"
	"go.uber.org/zap"
)

var (
	allEncodings = []common.IEncoding{
		&encodings.RawEncoding{},
		&encodings.TightEncoding{},
		&encodings.EncCursorPseudo{},
		&encodings.EncLedStatePseudo{},
		&encodings.TightPngEncoding{},
		&encodings.RREEncoding{},
		&encodings.ZLibEncoding{},
		&encodings.ZRLEEncoding{},
		&encodings.CopyRectEncoding{},
		&encodings.CoRREEncoding{},
		&encodings.HextileEncoding{},
	}
)

type VncProxy struct {
	Listener net.Listener
	Target   *Target

	RecordSession bool
	RecordingDir  string

	UpstreamVncPassword string // password to require of border0 clients
}

func (vp *VncProxy) createClientConnection(target *Target, encodings ...common.IEncoding) (*client.ClientConn, error) {
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", target.Hostname, target.Port))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to vnc server: %v", err)
	}
	clientConn, err := client.NewClientConn(
		conn,
		&client.ClientConfig{
			Auth: []client.ClientAuth{
				&client.PasswordAuth{Password: target.Password},
				&client.ClientAuthNone{},
			},
			Exclusive: true,
		},
		encodings...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create vnc client: %v", err)
	}
	return clientConn, nil
}

func (vp *VncProxy) newServerConnHandler(
	ctx context.Context,
	logger *zap.Logger,
	cfg *server.ServerConfig,
	sconn *server.ServerConn,
) error {
	cconn, err := vp.createClientConnection(vp.Target, allEncodings...)
	if err != nil {
		return fmt.Errorf("Proxy.newServerConnHandler error creating connection: %v", err)
	}
	if vp.RecordSession {
		recFile := "recording" + strconv.FormatInt(time.Now().Unix(), 10) + ".rbs"
		recPath := path.Join(vp.RecordingDir, recFile)
		rec, err := listeners.NewRecorder(recPath)
		if err != nil {
			return fmt.Errorf("failed to open recorder save path %s: %v", recPath, err)
		}
		sconn.Listeners.AddListener(rec)
		cconn.Listeners.AddListener(rec)
	}

	// gets the bytes from the actual vnc server on the env (client part of the proxy)
	// and writes them through the server socket to the vnc-client
	serverUpdater := &ServerUpdater{sconn}
	cconn.Listeners.AddListener(serverUpdater)

	// gets the messages from the server part (from vnc-client),
	// and write through the client to the actual vnc-server
	clientUpdater := &ClientUpdater{cconn}
	sconn.Listeners.AddListener(clientUpdater)

	if err = cconn.Connect(ctx, logger); err != nil {
		return fmt.Errorf("failed to connect to vnc target: %v", err)
	}
	return nil
}

func (vp *VncProxy) Serve(ctx context.Context, logger *zap.Logger) error {
	secHandlers := []server.SecurityHandler{&server.ServerAuthNone{}}
	if vp.UpstreamVncPassword != "" {
		secHandlers = []server.SecurityHandler{&server.ServerAuthVNC{Pass: vp.UpstreamVncPassword}}
	}
	cfg := &server.ServerConfig{
		SecurityHandlers: secHandlers,
		Encodings:        []common.IEncoding{&encodings.RawEncoding{}, &encodings.TightEncoding{}, &encodings.CopyRectEncoding{}},
		PixelFormat:      common.NewPixelFormat(32),
		ClientMessages:   server.DefaultClientMessages,
		DesktopName:      []byte("target"),
		Height:           uint16(768),
		Width:            uint16(1024),
		NewConnHandler:   vp.newServerConnHandler,
		UseDummySession:  true,
	}

	if err := server.Serve(ctx, logger, vp.Listener, cfg); err != nil {
		return fmt.Errorf("failed to serve vnc proxy: %v", err)
	}
	return nil
}
