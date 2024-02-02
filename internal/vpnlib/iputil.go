package vpnlib

import (
	"fmt"
	"net"
)

const (
	ipv4HeaderLengthBytes = 20
)

func cidrToUsableIPs(cidr string) ([]string, uint8, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid cidr range: %v", err)
	}

	subnetSize, _ := ipnet.Mask.Size()

	var ips []string
	for ip := ip.Mask(ipnet.Mask); ipnet.Contains(ip); incIp(ip) {
		ips = append(ips, ip.String())
	}

	// remove network and broadcast addresses
	if len(ips) > 2 {
		ips = ips[1 : len(ips)-1]
	}

	return ips, uint8(subnetSize), nil
}

func incIp(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

func parseIpFromPacketHeader(packet []byte) (net.IP, net.IP) {
	return net.IP(packet[12:16]), net.IP(packet[16:20])
}

func validateIPv4(packet []byte) error {
	if len(packet) < ipv4HeaderLengthBytes {
		return fmt.Errorf("packet too short")
	}
	ipVersion := (packet[0] & 0xF0) >> 4
	if ipVersion != 4 {
		return fmt.Errorf("packet header advertises non IPv4, version: %d", ipVersion)
	}
	return nil
}


// Returns a list of interfaces on which this IP network was found
func GetLocalInterfacesForIp(ipAddress string) ([]string, error) {
	//create a list of network interfaces, so we can return those if a match is found
	networkInterfaces := []string{}

	targetIP := net.ParseIP(ipAddress)
	if targetIP == nil {
		return networkInterfaces, fmt.Errorf("invalid IP address")
	}

	interfaces, err := net.Interfaces()
	if err != nil {
		return networkInterfaces, fmt.Errorf("failed to get local network interfaces: %v", err)
	}

	for _, iface := range interfaces {
		// Only consider interfaces that are up
		if iface.Flags&net.FlagUp == 0 {
			continue
		}

		addresses, err := iface.Addrs()
		if err != nil {
			return networkInterfaces, fmt.Errorf("failed to get addresses for interface %v: %v", iface.Name, err)
		}

		for _, address := range addresses {
			_, ipNet, err := net.ParseCIDR(address.String())
			if err != nil {
				continue
			}
			if ipNet.Contains(targetIP) {
				// append iface.Name to list of interfaces
				networkInterfaces = append(networkInterfaces, iface.Name)
			}
		}
	}
	return networkInterfaces, nil

}
