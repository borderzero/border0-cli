package session

import (
	"encoding/binary"
	"net"
)

type SessionHandler interface {
	Proxy(conn net.Conn)
}

func parseDims(b []byte) (uint32, uint32) {
	w := binary.BigEndian.Uint32(b)
	h := binary.BigEndian.Uint32(b[4:])
	return w, h
}
