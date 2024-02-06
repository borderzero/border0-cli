// Package vpnlib provides utilities for managing VPN connections over TLS sockets,
// Including IP address allocation, route management, and client to server communication
// over the VPN tunnel.
package vpnlib

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/borderzero/water"
)

const (
	// controlMessageHeaderByteSize is the number of bytes used for the header of the control message.
	controlMessageHeaderByteSize = 2
	// headerByteSize is the number of bytes used for the header.
	headerByteSize = 2
)

// ControlMessage represents a message used to tell clients
// the tunnel IPs and what routes to install on the interface.
type ControlMessage struct {
	ClientIp   string   `json:"client_ip"`
	ServerIp   string   `json:"server_ip"`
	SubnetSize uint8    `json:"subnet_size"`
	Routes     []string `json:"routes"` // CIDRs
}

// Build encodes a control message to ready-to-send bytes.
func (m *ControlMessage) Build() ([]byte, error) {
	controlMessageBytes, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("failed to encode control message to json: %v", err)
	}
	controlMessageHeader := make([]byte, controlMessageHeaderByteSize)
	binary.BigEndian.PutUint16(controlMessageHeader, uint16(len(controlMessageBytes)))
	return append(controlMessageHeader, controlMessageBytes...), nil
}

// GetControlMessage reads a control message from a net conn.
func GetControlMessage(conn net.Conn) (*ControlMessage, error) {
	controlMessageHeaderBuffer := make([]byte, controlMessageHeaderByteSize)

	// read first ${headerByteSize} bytes from the connection
	// to know how big the next incoming packet is
	headerN, err := conn.Read(controlMessageHeaderBuffer)
	if err != nil {
		return nil, fmt.Errorf("failed to read header: %v", err)
	}
	if headerN < controlMessageHeaderByteSize {
		return nil, fmt.Errorf("read less than controlMessageHeaderByteSize bytes (%d): %d", controlMessageHeaderByteSize, headerN)
	}

	// convert binary header to the size uint16
	inboundControlMessageSize := binary.BigEndian.Uint16(controlMessageHeaderBuffer)

	// new empty buffer of the size of the control message we're about to read
	controlMessageBuffer := make([]byte, inboundControlMessageSize)

	// read the control message
	controlMessageN, err := io.ReadFull(conn, controlMessageBuffer)
	if err != nil {
		return nil, fmt.Errorf("failed to read control message from net conn: %v", err)
	}
	if controlMessageN < int(inboundControlMessageSize) {
		return nil, fmt.Errorf("read less than the advertised control message size (expected %d, got %d)", inboundControlMessageSize, controlMessageN)
	}

	// decode control message JSON
	var ctrlMessage *ControlMessage
	if err = json.Unmarshal(controlMessageBuffer, &ctrlMessage); err != nil {
		return nil, fmt.Errorf("failed to decode control message JSON: %v", err)
	}

	return ctrlMessage, nil
}

func GetDefaultGateway(addressFamily int) (net.IP, string, error) {
	switch runtime.GOOS {
	case "darwin":
		return getDefaultGatewayDarwin(addressFamily)
	case "linux":
		return getDefaultGatewayLinux(addressFamily)
	case "windows":
		return getDefaultGatewayWindows(addressFamily)
	default:
		return nil, "", fmt.Errorf("runtime %s not supported", runtime.GOOS)
	}
}

func getDefaultGatewayDarwin(addressFamily int) (net.IP, string, error) {
	output, err := exec.Command("netstat", "-nr").Output()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get default route: %v", err)
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "default") {
			fields := strings.Fields(line)
			if len(fields) >= 4 {
				ip := net.ParseIP(fields[1])
				if ip != nil && ((addressFamily == 4 && ip.To4() != nil) || (addressFamily == 6 && ip.To4() == nil)) {
					return ip, fields[3], nil
				}
			}
		}
	}
	return nil, "", fmt.Errorf("default gateway not found")
}

func getDefaultGatewayLinux(addressFamily int) (net.IP, string, error) {
	output, err := exec.Command("ip", "route").Output()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get default route: %v", err)
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "default") {
			fields := strings.Fields(line)
			if len(fields) >= 5 {
				ip := net.ParseIP(fields[2])
				if ip != nil {
					return ip, fields[4], nil
				}
			}
		}
	}
	return nil, "", fmt.Errorf("default gateway not found")
}

func getDefaultGatewayWindows(addressFamily int) (net.IP, string, error) {
	output, err := exec.Command("cmd", "/C", "route print 0.0.0.0").Output()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get default route: %v", err)
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line) // Remove leading and trailing whitespace
		if strings.HasPrefix(trimmedLine, "0.0.0.0") {
			fields := strings.Fields(trimmedLine) // Splits the trimmed line
			if len(fields) >= 5 {
				ip := net.ParseIP(fields[2])
				if ip != nil {
					return ip, fields[3], nil
				}
			}
		}
	}
	return nil, "", fmt.Errorf("default gateway not found")
}

// AddRoutesViaGateway adds routes through a specified gateway IP.
func AddRoutesViaGateway(gateway string, routes []string) error {
	switch runtime.GOOS {
	case "darwin":
		return addRoutesViaGatewayDarwin(gateway, routes)
	case "linux":
		return addRoutesViaGatewayLinux(gateway, routes)
	case "windows":
		return addRoutesViaGatewayWindows(gateway, routes)
	default:
		return fmt.Errorf("runtime %s not supported", runtime.GOOS)
	}
}

func addRoutesViaGatewayDarwin(gateway string, routes []string) error {
	for _, route := range routes {
		if err := exec.Command("route", "-n", "add", "-net", route, gateway).Run(); err != nil {
			return fmt.Errorf("error adding route %s via gateway %s: %v", route, gateway, err)
		}
	}
	return nil
}

func addRoutesViaGatewayLinux(gateway string, routes []string) error {
	for _, route := range routes {
		if err := exec.Command("ip", "route", "add", route, "via", gateway).Run(); err != nil {
			return fmt.Errorf("error adding route %s via gateway %s: %v", route, gateway, err)
		}
	}
	return nil
}

func addRoutesViaGatewayWindows(gateway string, routes []string) error {
	for _, route := range routes {
		var network, mask string

		// Check if route is in CIDR notation
		if _, ipNet, err := net.ParseCIDR(route); err == nil {
			// CIDR notation
			network = ipNet.IP.String()
			mask = net.IP(ipNet.Mask).String()
		} else if ip := net.ParseIP(route); ip != nil {
			// Single IP Address
			network = ip.String()
			mask = "255.255.255.255" // Subnet mask for a single IP
		} else {
			// Invalid input
			return fmt.Errorf("invalid route (not CIDR or IP) %s: %v", route, err)
		}

		// Execute the route add command
		if err := exec.Command("route", "add", network, "mask", mask, gateway).Run(); err != nil {
			return fmt.Errorf("error adding route %s via gateway %s: %v", route, gateway, err)
		}
	}
	return nil
}

// DeleteRoutesViaGateway removes routes that go through a specified gateway IP.
func DeleteRoutesViaGateway(gateway string, routes []string) error {
	switch runtime.GOOS {
	case "darwin":
		return deleteRoutesViaGatewayDarwin(gateway, routes)
	case "linux":
		return deleteRoutesViaGatewayLinux(gateway, routes)
	case "windows":
		return deleteRoutesViaGatewayWindows(gateway, routes)
	default:
		return fmt.Errorf("runtime %s not supported", runtime.GOOS)
	}
}

func deleteRoutesViaGatewayDarwin(gateway string, routes []string) error {
	for _, route := range routes {
		if err := exec.Command("route", "delete", "-net", route, gateway).Run(); err != nil {
			return fmt.Errorf("error deleting route %s via gateway %s: %v", route, gateway, err)
		}
	}
	return nil
}

func deleteRoutesViaGatewayLinux(gateway string, routes []string) error {
	for _, route := range routes {
		if err := exec.Command("ip", "route", "del", route, "via", gateway).Run(); err != nil {
			return fmt.Errorf("error deleting route %s via gateway %s: %v", route, gateway, err)
		}
	}
	return nil
}

func deleteRoutesViaGatewayWindows(gateway string, routes []string) error {
	for _, route := range routes {

		var network, mask string

		// Check if route is in CIDR notation
		if _, ipNet, err := net.ParseCIDR(route); err == nil {
			// CIDR notation
			network = ipNet.IP.String()
			mask = net.IP(ipNet.Mask).String()
		} else if ip := net.ParseIP(route); ip != nil {
			// Single IP Address
			network = ip.String()
			mask = "255.255.255.255" // Subnet mask for a single IP
		} else {
			// Invalid input
			return fmt.Errorf("invalid route (not CIDR or IP) %s: %v", route, err)
		}

		if err := exec.Command("route", "delete", network, "mask", mask, gateway).Run(); err != nil {
			return fmt.Errorf("error deleting route %s via gateway %s: %v", route, gateway, err)
		}
	}
	return nil
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
		return fmt.Errorf("runtime %s not supported", runtime.GOOS)
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
			fmt.Println("ip", "route", "add", route, "dev", iface)
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

func AddServerIp(iface, localIp string, subnetSize uint8) error {
	switch runtime.GOOS {
	case "darwin":
		return AddServerIpDarwin(iface, localIp, subnetSize)
	case "linux":
		return AddServerIpLinux(iface, localIp, subnetSize)
	// case "windows":
	// 	return AddServerIpWindows(iface, localIp)
	default:
		return fmt.Errorf("runtime %s not supported", runtime.GOOS)
	}
}

func AddServerIpDarwin(iface, localIp string, subnetSize uint8) error {
	if err := exec.Command("ifconfig", iface, "mtu", "1200").Run(); err != nil {
		return fmt.Errorf("error setting MTU for interface %s: %v", iface, err)
	}
	// calculate p2p ip address from localIP take one digit lower
	ip := net.ParseIP(localIp)
	newIP := ip.To4()
	newIP[3] = newIP[3] - 1

	ipAddress := fmt.Sprintf("%s/%d", localIp, subnetSize)
	if err := exec.Command("ifconfig", iface, ipAddress, newIP.String(), "up").Run(); err != nil {
		fmt.Println("ifconfig ", iface, " ", ipAddress, " ", newIP.String(), " up")
		return fmt.Errorf("error adding tunnel %s to interface %s: %v", ipAddress, iface, err)
	}
	return nil
}

func AddServerIpLinux(iface, localIp string, subnetSize uint8) error {
	serverIp := fmt.Sprintf("%s/%d", localIp, subnetSize)
	if err := exec.Command("ip", "addr", "add", serverIp, "dev", iface).Run(); err != nil {
		return fmt.Errorf("error adding ip %s to interface %s: %v", localIp, iface, err)
	}
	if err := exec.Command("ip", "link", "set", "dev", iface, "up", "mtu", "1200").Run(); err != nil {
		return fmt.Errorf("error setting link for interface %s: %v", iface, err)
	}
	return nil
}

// AddIpToIface adds an ip address to a network interface.
func AddIpToIface(iface, localIp, remoteIp string, subnetSize uint8) error {
	switch runtime.GOOS {
	case "darwin":
		return addIpToIfaceDarwin(iface, localIp, remoteIp)
	case "linux":
		return addIpToIfaceLinux(iface, localIp, subnetSize)
	case "windows":
		return addIpToIfaceWindows(iface, localIp, subnetSize)
	default:
		return fmt.Errorf("runtime %s not supported", runtime.GOOS)
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

func addIpToIfaceLinux(iface, localIp string, subnetSize uint8) error {
	if err := exec.Command("ip", "addr", "add", fmt.Sprintf("%s/%d", localIp, subnetSize), "dev", iface).Run(); err != nil {
		return fmt.Errorf("error adding ip %s to interface %s: %v", localIp, iface, err)
	}
	if err := exec.Command("ip", "link", "set", "dev", iface, "up", "mtu", "1200").Run(); err != nil {
		return fmt.Errorf("error setting link for interface %s: %v", iface, err)
	}
	return nil
}

func addIpToIfaceWindows(iface, localIp string, subnetSize uint8) error {
	// Convert subnet size to subnet mask
	var subnetMask net.IP = make(net.IP, net.IPv4len)
	for i := uint8(0); i < subnetSize; i++ {
		subnetMask[i/8] |= 1 << (7 - i%8)
	}

	if err := exec.Command("netsh", "interface", "ip", "set", "address", iface, "static", localIp, subnetMask.String()).Run(); err != nil {
		return fmt.Errorf("error adding IP %s with subnet mask %s to interface %s: %v", localIp, subnetMask.String(), iface, err)
	}
	return nil
}

// Start Tun to Conn thread

func TunToConnCopy(iface *water.Interface, cm *ConnectionMap, returnOnErr bool, conn net.Conn) error {
	packetBufferSize := 9000
	packetbuffer := make([]byte, packetBufferSize)
	sizeBuf := make([]byte, headerByteSize)
	var recipientConn net.Conn

	for {
		// read one packet from the TUN iface
		n, err := iface.Read(packetbuffer)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				fmt.Printf("TUN iface closed, exiting\n")
				return err
			}
			// If error contails "file already closed" then we can return
			// because the connection was closed
			if strings.Contains(err.Error(), "file already closed") {
				if returnOnErr {
					return err
				} else {
					continue
				}
			}
			fmt.Printf("Failed to read packet from the TUN iface: %v %s\n", err, err)
			continue
		}

		// Identify the recipient IP from the packetBuffer
		// with that IP, find the net conn in the connection map

		packet := packetbuffer[:n] // assuming packetbuffer contains an IP packet

		// for now ignore ipv6 packets
		ipVersion := (packet[0] & 0xF0) >> 4
		if ipVersion != 4 {
			continue
		}
		if err := validateIPv4(packet); err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}
		_, dstIp := parseIpFromPacketHeader(packet)

		// Check if conn is not nil
		if conn == nil {
			// we didnt get a net.Conn object, so we need to find it in the connection map
			// This happens only on the server side
			// Clients provide the conn object

			// TODO: maybe we should use the net.IP object instead of string
			// to avoid the string conversion, maybe it's not a big deal... not sure?
			var exists bool

			if recipientConn, exists = cm.Get(dstIp.String()); !exists {
				//fmt.Printf("No connection exists for IP: %s\n", dstIp)
				continue
			}
		} else {
			recipientConn = conn
		}

		// compute the header to prepend to the packet before writing to net conn

		binary.BigEndian.PutUint16(sizeBuf, uint16(n))

		// write the encapsulated packet to the net conn

		_, err = recipientConn.Write(append(sizeBuf, packetbuffer[:n]...))
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				recipientConn.Close()
				cm.Delete(dstIp.String())

				if returnOnErr {
					return err
				}
			}
			fmt.Printf("Failed to write encapsulated packet to the net conn: %v\n", err)
			recipientConn.Close()
			cm.Delete(dstIp.String())
			if returnOnErr {
				return err
			} else {
				continue
			}
		}
	}
}

func ConnToTunCopy(conn net.Conn, iface *water.Interface) error {
	headerBuffer := make([]byte, headerByteSize)

	for {
		// read first ${headerByteSize} bytes from the connection
		// to know how big the next incoming packet is.
		// Make sure we read all the way to the end of the header using io.ReadFull()
		headerN, err := io.ReadFull(conn, headerBuffer)
		if err != nil {
			if errors.Is(err, io.EOF) ||
				errors.Is(err, io.ErrUnexpectedEOF) {
				fmt.Println("Connection closed by remote host")
				return err
			} else {
				fmt.Printf("Failed to read header: %v\n", err)
				return err
			}

		}
		if headerN < headerByteSize {
			fmt.Printf("Read less than headerByteSize bytes (%d): %d\n", headerByteSize, headerN)
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

		// write the packet to the TUN iface
		if _, err = iface.Write(packetBuffer); err != nil {
			fmt.Printf("Failed to write packet to the TUN iface: %v\n", err)
			continue
		}
	}
}

func CheckIPForwardingEnabled() bool {
	// Path to the ip_forward configuration
	const path = "/proc/sys/net/ipv4/ip_forward"

	// Read the contents of the file using os.ReadFile
	content, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("Failed to read %s: %v", path, err)
	}

	// The file should contain a single character: '1' or '0'
	// TrimSpace is not shown here, but you could use strings.TrimSpace if needed
	isEnabled := string(content[0]) == "1"

	return isEnabled
}
