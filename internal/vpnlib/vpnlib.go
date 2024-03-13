// Package vpnlib provides utilities for managing VPN connections over TLS sockets,
// Including IP address allocation, route management, and client to server communication
// over the VPN tunnel.
package vpnlib

import (
	"context"
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
)

const (
	// controlMessageHeaderByteSize is the number of bytes used for the header of the control message.
	controlMessageHeaderByteSize = 2
	// border0HeaderByteSize is the number of bytes used for the header we use in each packet for reconstruction.
	border0HeaderByteSize = 2
)

// ControlMessage represents a message used to tell clients
// the tunnel IPs and what routes to install on the interface.
type ControlMessage struct {
	ClientIp   string   `json:"client_ip"`
	ServerIp   string   `json:"server_ip"`
	SubnetSize uint8    `json:"subnet_size"`
	Routes     []string `json:"routes,omitempty"` // CIDRs
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

// TunToConnCopy reads packets and fowards them to the given connection. This
// function is used by the VPN "clients" and must *not* be used by the server.
// This function is *not* resilient to errors and will return upon encountering
// a read/write error or if the context is cancelled.
func TunToConnCopy(ctx context.Context, source io.Reader, conn net.Conn) error {
	packetBufferSize := 9000
	packetbuffer := make([]byte, packetBufferSize)
	b0HeaderBuffer := make([]byte, border0HeaderByteSize)

	for {
		select {
		case <-ctx.Done():
			if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
			return nil
		default:
			// read one packet from the source
			n, err := source.Read(packetbuffer)
			if err != nil {
				if !errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					return nil // source was closed, have to return
				}
				fmt.Printf("WARNING: failed to read packet: %v\n", err)
				continue
			}
			packet := packetbuffer[:n]

			// ignore non IPv4 packets
			ipVersion := (packet[0] & 0xF0) >> 4
			if ipVersion != 4 {
				continue
			}
			if err := validateIPv4(packet); err != nil {
				fmt.Printf("WARNING: received invalid IPv4 packet: %v\n", err)
				continue
			}

			// we produce a "border0 header" so that we can write a single packet
			// across multiple connection writes (under the hood) if needed.
			binary.BigEndian.PutUint16(b0HeaderBuffer, uint16(n))

			_, err = conn.Write(append(b0HeaderBuffer, packetbuffer[:n]...))
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					return err
				}
				fmt.Printf("Failed to write encapsulated packet to the net conn: %v\n", err)
			}
		}

	}
}

// TunToConnMapCopy reads packets and fowards them to the appropriate connection in a ConnectionMap.
// This function is used by the VPN "server" and must *not* be used by clients. This function is resilient
// to errors and will run for as long as the source is not closed and the context is not cancelled.
func TunToConnMapCopy(ctx context.Context, source io.Reader, dstMap *ConnectionMap) error {
	packetBufferSize := 9000
	packetbuffer := make([]byte, packetBufferSize)
	b0HeaderBuffer := make([]byte, border0HeaderByteSize)

	for {
		select {
		case <-ctx.Done():
			if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
			return nil
		default:
			// read one packet from the source
			n, err := source.Read(packetbuffer)
			if err != nil {
				if !errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					return nil // source was closed, have to return
				}
				fmt.Printf("WARNING: failed to read packet: %v\n", err)
				continue
			}
			packet := packetbuffer[:n]

			// ignore non IPv4 packets
			ipVersion := (packet[0] & 0xF0) >> 4
			if ipVersion != 4 {
				continue
			}
			if err := validateIPv4(packet); err != nil {
				fmt.Printf("WARNING: received invalid IPv4 packet: %v\n", err)
				continue
			}

			_, dstIp := parseIpFromPacketHeader(packet)
			dstIpString := dstIp.String()

			if dstConn, exists := dstMap.Get(dstIpString); exists {

				// we produce a "border0 header" so that we can write a single packet
				// across multiple connection writes (under the hood) if needed.
				binary.BigEndian.PutUint16(b0HeaderBuffer, uint16(n))

				// write packet to target connection
				_, err = dstConn.Write(append(b0HeaderBuffer, packetbuffer[:n]...))
				if err != nil {
					// if there's any errors, we kick the client out
					dstConn.Close()
					dstMap.Delete(dstIpString)

					if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
						fmt.Printf("INFO: client %s dropped\n", dstIpString)
					} else {
						fmt.Printf("WARNING: client %s kicked due to error: %v\n", dstIpString, err)
					}
				}
			} else {
				fmt.Printf("WARNING: received IPv4 for invalid destination address %s: %v\n", dstIpString, err)
			}
		}
	}
}

func ConnToTunCopy(ctx context.Context, conn net.Conn, tun io.Writer, middlewares ...PacketMiddleware) error {
	b0HeaderBuffer := make([]byte, border0HeaderByteSize)

	for {
		select {
		case <-ctx.Done():
			if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
			return nil
		default:
			// read first ${headerByteSize} bytes from the connection
			// to know how big the next incoming packet is.
			// Make sure we read all the way to the end of the header using io.ReadFull()
			headerN, err := io.ReadFull(conn, b0HeaderBuffer)
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					fmt.Println("Connection closed by remote host")
					return nil
				}
				if errors.Is(err, net.ErrClosed) {
					return err // this means the client side closed the connection
				}
				// we return error here because we don't know how to
				// proceed in the packet stream...
				fmt.Printf("Failed to read header: %v\n", err)
				return err
			}
			if headerN < border0HeaderByteSize {
				fmt.Printf("Read less than border0HeaderByteSize bytes (%d): %d\n", border0HeaderByteSize, headerN)
				continue
			}

			// convert binary header to the size uint16
			inboundPacketSize := binary.BigEndian.Uint16(b0HeaderBuffer)

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

			// run middlewares
			for _, mw := range middlewares {
				copiedPck := make([]byte, packetN)
				copy(copiedPck, packetBuffer)
				go mw(copiedPck)
			}

			// write the packet to the TUN iface
			if _, err = tun.Write(packetBuffer); err != nil {
				fmt.Printf("Failed to write packet to the TUN iface: %v\n", err)
				continue
			}

		}
	}
}

func CheckIPForwardingEnabled() (bool, error) {
	// Path to the ip_forward configuration
	const path = "/proc/sys/net/ipv4/ip_forward"

	// Read the contents of the file using os.ReadFile
	content, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("failed to read %s: %v", path, err)
	}

	// The file should contain a single character: '1' or '0'
	// TrimSpace is not shown here, but you could use strings.TrimSpace if needed
	isEnabled := string(content[0]) == "1"

	return isEnabled, nil
}

func GetDnsByPassRoutes(vpnIfaceName string, routes []string, addressFamily int) (map[string]bool, error) {

	// make a map we'll use for DNS bypass routes
	// This will contain a list of unique DNS servers for which we'll do bypass routes
	dnsServersBypassRoutes := make(map[string]bool)

	// Also get the DNS servers from the server.
	// We may need to create bypass routes for these
	currentDnsServers, err := GetDnsServers()
	if err != nil {
		return dnsServersBypassRoutes, fmt.Errorf("failed to get current DNS servers %v", err)
	}
	for _, route := range routes {

		for _, dnsServer := range currentDnsServers {

			// turn dnsserver into a net.IP
			dnsIp := net.ParseIP(dnsServer)
			// for now we only support ipv4
			if addressFamily == 4 && dnsIp.To4() != nil {
				match, _ := IsIPInCIDR(dnsServer, route)
				if match {
					if dnsIp.IsLoopback() {
						// If it's loopback, dont; do anything
						continue
					} else if dnsIp.IsPrivate() {
						networkInterfaces, err := GetLocalInterfacesForIp(dnsServer)
						if err != nil {
							log.Println("failed to check if IP is local", err)
							continue
						}
						// if the map is not empty, then the IP is local
						if len(networkInterfaces) > 0 {
							// This is the case if the IP is on the same subnet as the local gateway, for example on the router
							// However we should make sure it's not on the newly added tun VPN interface

							// we should make sure the list of interfaces is not empty, and doesn't contain iface.Name()
							// if it does, we should continue
							// check the list to and if the interface found is not the same as  iface.Name()
							// Which is the VPN tunnel, then it's a local network, and we should continue
							// ie. no by pass route is needed, since the IP address is locally connected.

							found := false
							for _, name := range networkInterfaces {
								if name != vpnIfaceName {
									// network found, not adding bypass route
									found = true
								}
							}
							if !found {
								// if we get here, then the IP is:
								// 1) not loopback (systemd uses 127.0.0.53)
								// 2) not on a local network (ie. not on the same subnet as any of the interfaces)
								// 3) is RFC1918 (private) address
								// So we should add a bypass route for it
								// we use a map to make sure we only add each DNS server once
								// Example case, in coffeee shop, my ip is 10.10.10.10,
								// and the DNS server is 10.11.11.11 , ie different subnet, but wont work on vpn
								dnsServersBypassRoutes[fmt.Sprintf("%s/%d", dnsIp.String(), 32)] = true
							}
						}
					}
				}
			}
		}
	}
	return dnsServersBypassRoutes, nil
}
