package vpnlib

// PacketMiddleware represents a middleware function
// to be invoked on any packet read from a client conn.
type PacketMiddleware func(pk []byte)
