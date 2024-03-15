package proxy

// Target represents the VNC server
// we wish to proxy traffic to.
type Target struct {
	Hostname string
	Port     uint16
	Password string
}
