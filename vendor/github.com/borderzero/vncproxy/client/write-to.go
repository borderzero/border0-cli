package client

import (
	"fmt"
	"io"

	"github.com/borderzero/vncproxy/common"
)

type WriteTo struct {
	Writer io.Writer
	Name   string
}

func (p *WriteTo) Consume(seg *common.RfbSegment) error {
	switch seg.SegmentType {
	case common.SegmentMessageStart:
	case common.SegmentRectSeparator:
	case common.SegmentBytes:
		_, err := p.Writer.Write(seg.Bytes)
		if err != nil {
			return fmt.Errorf("failed to write segment of type %s to port: %v", seg.SegmentType.String(), err)
		}
		return err
	case common.SegmentFullyParsedClientMessage:
		clientMsg := seg.Message.(common.ClientMessage)
		if err := clientMsg.Write(p.Writer); err != nil {
			return fmt.Errorf("failed to write segment of type %s to port: %v", seg.SegmentType.String(), err)
		}
		return nil
	default:
		return fmt.Errorf("unknown RFB segment type %s", seg.SegmentType.String())
	}
	return nil
}
