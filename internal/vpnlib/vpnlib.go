package vpnlib

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"runtime"
	"time"

	"github.com/songgao/water"
	"golang.org/x/sync/semaphore"
)

const (
	controlHeaderByteSize       = 2
	packetHeaderBufferByteSize  = 2
	packetBufferByteSize        = 9000
	packetWritersMaxConcurrency = 50
)

// ControlMessage represents a message used to tell clients
// the tunnel IPs and what routes to install on the interface.
type ControlMessage struct {
	ClientIp     string   `json:"client_ip"`
	ServerIp     string   `json:"server_ip"`
	SubnetLength uint8    `json:"subnet_length"`
	Routes       []string `json:"routes"` // CIDRs
}

// Build encodes a control message to ready-to-send bytes.
func (m *ControlMessage) Build() ([]byte, error) {
	controlMessageBytes, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("failed to encode control message to json: %v", err)
	}
	controlMessageHeader := make([]byte, controlHeaderByteSize)
	binary.BigEndian.PutUint16(controlMessageHeader, uint16(len(controlMessageBytes)))
	return append(controlMessageHeader, controlMessageBytes...), nil
}

// GetControlMessage reads a control message from a net conn.
func GetControlMessage(conn net.Conn, timeout time.Duration) (*ControlMessage, error) {
	conn.SetReadDeadline(time.Now().Add(timeout)) // note: ignored error
	defer conn.SetDeadline(time.Time{})           // note: ignored error

	controlMessageHeaderBuffer := make([]byte, controlHeaderByteSize)

	// read first ${headerByteSize} bytes from the connection
	// to know how big the next incoming packet is
	headerN, err := io.ReadFull(conn, controlMessageHeaderBuffer)
	if err != nil {
		return nil, fmt.Errorf("Failed to read header: %v", err)
	}
	if headerN < controlHeaderByteSize {
		return nil, fmt.Errorf("Read less than controlMessageHeaderByteSize bytes (%d): %d", controlHeaderByteSize, headerN)
	}

	// convert binary header to the size uint16
	inboundControlMessageSize := binary.BigEndian.Uint16(controlMessageHeaderBuffer)

	// new empty buffer of the size of the control message we're about to read
	controlMessageBuffer := make([]byte, inboundControlMessageSize)

	// read the control message
	controlMessageN, err := io.ReadFull(conn, controlMessageBuffer)
	if err != nil {
		return nil, fmt.Errorf("Failed to read control message from net conn: %v", err)
	}
	if controlMessageN < int(inboundControlMessageSize) {
		return nil, fmt.Errorf("Read less than the advertised control message size (expected %d, got %d)", inboundControlMessageSize, controlMessageN)
	}

	// decode control message JSON
	var ctrlMessage *ControlMessage
	if err = json.Unmarshal(controlMessageBuffer, &ctrlMessage); err != nil {
		return nil, fmt.Errorf("Failed to decode control message JSON: %v", err)
	}

	return ctrlMessage, nil
}

// AddRoutesToIface adds routes to a network interface.
func AddRoutesToIface(iface string, routes []string) error {
	switch runtime.GOOS {
	case "darwin":
		return addRoutesToIfaceDarwin(iface, routes)
	case "linux":
		return addRoutesToIfaceLinux(iface, routes)
	case "windows":
		return addRoutesToIfaceWindows(iface, routes)
	default:
		return fmt.Errorf("Runtime %s not supported", runtime.GOOS)
	}
}

func addRoutesToIfaceDarwin(iface string, routes []string) error {
	for _, route := range routes {
		if err := exec.Command("route", "-n", "add", "-net", route, "-interface", iface).Run(); err != nil {
			return fmt.Errorf("error adding route %s to interface %s: %v", route, iface, err)
		}
	}
	return nil
}

func addRoutesToIfaceLinux(iface string, routes []string) error {
	for _, route := range routes {
		if err := exec.Command("ip", "route", "add", route, "dev", iface).Run(); err != nil {
			return fmt.Errorf("error adding route %s to interface %s: %v", route, iface, err)
		}
	}
	return nil
}

func addRoutesToIfaceWindows(iface string, routes []string) error {
	for _, route := range routes {
		// Convert route in CIDR notation to network and mask.
		_, ipNet, err := net.ParseCIDR(route)
		if err != nil {
			return fmt.Errorf("invalid CIDR notation %s: %v", route, err)
		}
		network := ipNet.IP.String()
		mask := net.IP(ipNet.Mask).String()

		if err := exec.Command("route", "add", network, "mask", mask, iface).Run(); err != nil {
			return fmt.Errorf("error adding route %s to interface %s: %v", route, iface, err)
		}
	}
	return nil
}

// AddIpToIface adds an ip address to a network interface.
func AddIpToIface(iface, localIp, remoteIp string) error {
	switch runtime.GOOS {
	case "darwin":
		return addIpToIfaceDarwin(iface, localIp, remoteIp)
	case "linux":
		return addIpToIfaceLinux(iface, localIp)
	case "windows":
		return addIpToIfaceWindows(iface, localIp)
	default:
		return fmt.Errorf("Runtime %s not supported", runtime.GOOS)
	}
}

func addIpToIfaceDarwin(iface, localIp, remoteIp string) error {
	if err := exec.Command("ifconfig", iface, "mtu", "1200").Run(); err != nil {
		return fmt.Errorf("error setting MTU for interface %s: %v", iface, err)
	}

	if err := exec.Command("ifconfig", iface, localIp, remoteIp, "up").Run(); err != nil {
		return fmt.Errorf("error adding tunnel [ %s <--> %s ] to interface %s: %v", localIp, remoteIp, iface, err)
	}
	return nil
}

func addIpToIfaceLinux(iface, localIp string) error {
	if err := exec.Command("ip", "addr", "add", fmt.Sprintf("%s/30", localIp), "dev", iface).Run(); err != nil {
		return fmt.Errorf("error adding ip %s to interface %s: %v", localIp, iface, err)
	}
	if err := exec.Command("ip", "link", "set", "dev", iface, "up", "mtu", "1200").Run(); err != nil {
		return fmt.Errorf("error setting link for interface %s: %v", iface, err)
	}
	return nil
}

func addIpToIfaceWindows(iface, localIp string) error {
	if err := exec.Command("netsh", "interface", "ip", "set", "address", iface, "static", localIp).Run(); err != nil {
		return fmt.Errorf("error adding ip %s to interface %s: %v", localIp, iface, err)
	}
	return nil
}

// PacketCopy copies packets between a net.Conn and a TUN interface.
func PacketCopy(conn net.Conn, iface *water.Interface) error {
	ctx := context.TODO()
	headerBuffer := make([]byte, packetHeaderBufferByteSize)
	packetbuffer := make([]byte, packetBufferByteSize)

	sem := semaphore.NewWeighted(packetWritersMaxConcurrency)

	go func() {
		for {
			// read first ${headerByteSize} bytes from the connection
			// to know how big the next incoming packet is
			headerN, err := conn.Read(headerBuffer)
			if err != nil {
				if errors.Is(err, io.EOF) ||
					errors.Is(err, io.ErrUnexpectedEOF) {
					return
				}
				fmt.Printf("Failed to read header: %v\n", err)
				continue
			}
			if headerN < packetHeaderBufferByteSize {
				fmt.Printf("Read less than packetHeaderBufferByteSize bytes (%d < %d)\n", packetHeaderBufferByteSize, headerN)
				continue
			}

			// convert binary header to the size uint16
			inboundPacketSize := binary.BigEndian.Uint16(headerBuffer)

			// new empty buffer of the size of the packet we're about to read
			packetBuffer := make([]byte, inboundPacketSize)

			// read the one individual packet
			packetN, err := io.ReadFull(conn, packetBuffer)
			if err != nil {
				fmt.Printf("Failed to read packet from net conn: %v\n", err)
				continue
			}
			if packetN < int(inboundPacketSize) {
				fmt.Printf("Read less than the advertised packet size (expected %d, got %d)\n", inboundPacketSize, packetN)
				continue
			}

			sem.Acquire(ctx, 1)
			go func() {
				defer sem.Release(1)

				// write the packate to the TUN iface
				if _, err = iface.Write(packetBuffer); err != nil {
					fmt.Printf("Failed to write packet to the TUN iface: %v\n", err)
					return
				}
			}()
		}
	}()

	for {
		// read one packet from the TUN iface
		n, err := iface.Read(packetbuffer)
		if err != nil {
			fmt.Printf("Failed to read packet from the TUN iface: %v\n", err)
			continue
		}

		// compute the header to prepend to the packet before writing to net conn
		sizeBuf := make([]byte, packetHeaderBufferByteSize)
		binary.BigEndian.PutUint16(sizeBuf, uint16(n))

		// write the encapsulated packet to the net conn
		_, err = conn.Write(append(sizeBuf, packetbuffer[:n]...))
		if err != nil {
			fmt.Printf("Failed to write encapsulated packet to the net conn: %v\n", err)
			continue
		}
	}
}
