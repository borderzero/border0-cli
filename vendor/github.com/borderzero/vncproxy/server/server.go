package server

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/borderzero/vncproxy/common"
	"go.uber.org/zap"
)

var DefaultClientMessages = []common.ClientMessage{
	&MsgSetPixelFormat{},
	&MsgSetEncodings{},
	&MsgFramebufferUpdateRequest{},
	&MsgKeyEvent{},
	&MsgPointerEvent{},
	&MsgClientCutText{},
	&MsgClientQemuExtendedKey{},
}

// FramebufferUpdate holds a FramebufferUpdate wire format message.
type FramebufferUpdate struct {
	_       [1]byte             // padding
	NumRect uint16              // number-of-rectangles
	Rects   []*common.Rectangle // rectangles
}

type ServerHandler func(context.Context, *zap.Logger, *ServerConfig, *ServerConn) error

type ServerConfig struct {
	SecurityHandlers []SecurityHandler
	Encodings        []common.IEncoding
	PixelFormat      *common.PixelFormat
	ColorMap         *common.ColorMap
	ClientMessages   []common.ClientMessage
	DesktopName      []byte
	Height           uint16
	Width            uint16
	UseDummySession  bool

	//handler to allow for registering for messages, this can't be a channel
	//because of the websockets handler function which will kill the connection on exit if conn.handle() is run on another thread
	NewConnHandler ServerHandler
}

func Serve(ctx context.Context, logger *zap.Logger, ln net.Listener, cfg *ServerConfig) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		go attachNewServerConn(ctx, logger, c, cfg, "dummySession")
	}
}

func attachNewServerConn(
	ctx context.Context,
	logger *zap.Logger,
	c io.ReadWriter,
	cfg *ServerConfig,
	sessionId string,
) error {
	conn, err := NewServerConn(c, cfg)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := ServerVersionHandler(cfg, conn); err != nil {
		return err
	}

	if err := ServerSecurityHandler(cfg, conn); err != nil {
		return err
	}

	//run the handler for this new incoming connection from a vnc-client
	//this is done before the init sequence to allow listening to server-init messages (and maybe even interception in the future)
	err = cfg.NewConnHandler(ctx, logger, cfg, conn)
	if err != nil {
		return err
	}

	if err := ServerClientInitHandler(cfg, conn); err != nil {
		return err
	}

	if err := ServerServerInitHandler(cfg, conn); err != nil {
		return err
	}

	conn.SessionId = sessionId
	if cfg.UseDummySession {
		conn.SessionId = "dummySession"
	}

	return conn.handle()
}
