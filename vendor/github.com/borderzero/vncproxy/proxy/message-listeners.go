package proxy

import (
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/borderzero/vncproxy/client"
	"github.com/borderzero/vncproxy/common"
	"github.com/borderzero/vncproxy/server"
)

type ClientUpdater struct {
	conn *client.ClientConn
}

// Consume recieves vnc-server-bound messages (Client messages) and updates the server part of the proxy
func (cc *ClientUpdater) Consume(seg *common.RfbSegment) error {
	switch seg.SegmentType {

	case common.SegmentFullyParsedClientMessage:
		clientMsg := seg.Message.(common.ClientMessage)
		switch clientMsg.Type() {

		case common.SetPixelFormatMsgType:
			// update pixel format
			pixFmtMsg := clientMsg.(*server.MsgSetPixelFormat)
			cc.conn.PixelFormat = pixFmtMsg.PF
		}
		if err := clientMsg.Write(cc.conn); err != nil {
			return fmt.Errorf("ClientUpdater.Consume (vnc-server-bound, SegmentFullyParsedClientMessage): problem writing to port: %s", err)
		}
		return nil
	}
	return nil
}

type ServerUpdater struct {
	conn *server.ServerConn
}

func (p *ServerUpdater) Consume(seg *common.RfbSegment) error {
	switch seg.SegmentType {
	case common.SegmentMessageStart:
	case common.SegmentRectSeparator:
	case common.SegmentServerInitMessage:
		serverInitMessage := seg.Message.(*common.ServerInit)
		p.conn.SetHeight(serverInitMessage.FBHeight)
		p.conn.SetWidth(serverInitMessage.FBWidth)
		p.conn.SetDesktopName(string(serverInitMessage.NameText))
		p.conn.SetPixelFormat(&serverInitMessage.PixelFormat)

	case common.SegmentBytes:
		if _, err := p.conn.Write(seg.Bytes); err != nil {
			// this connection is closed, just return
			if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("WriteTo.Consume (ServerUpdater SegmentBytes): problem writing to port: %s", err)
		}
		return nil
	case common.SegmentFullyParsedClientMessage:
		clientMsg := seg.Message.(common.ClientMessage)
		if err := clientMsg.Write(p.conn); err != nil {
			return fmt.Errorf("WriteTo.Consume (ServerUpdater SegmentFullyParsedClientMessage): problem writing to port: %s", err)
		}
		return nil
	default:
		return errors.New("WriteTo.Consume: undefined RfbSegment type")
	}
	return nil
}
