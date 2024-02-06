//go:build !windows
// +build !windows

package vpnlib

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// GetDnsServers returns a list of all active resolvers used by the system
func GetDnsServers() ([]string, error) {
	switch runtime.GOOS {
	case "darwin":
		return getDnsDarwin()
	case "linux":
		return getDnsLinux()

	default:
		return nil, fmt.Errorf("runtime %s not supported", runtime.GOOS)
	}
}

func getDnsDarwin() ([]string, error) {
	out, err := exec.Command("scutil", "--dns").Output()
	if err != nil {
		return nil, fmt.Errorf("error getting DNS servers: %v", err)
	}

	var servers []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "nameserver") {
			ip := strings.TrimSpace(strings.Split(line, ":")[1])
			if net.ParseIP(ip) != nil {
				servers = append(servers, ip)
			}

		}
	}
	return servers, nil
}

func getDnsLinux() ([]string, error) {
	return []string{}, nil
	file, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return nil, fmt.Errorf("error getting DNS servers: %v", err)
	}
	defer file.Close()

	var servers []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "nameserver") {
			ip := strings.TrimSpace(strings.Split(line, " ")[1])
			if net.ParseIP(ip) != nil {
				servers = append(servers, ip)
			}

		}
	}
	return servers, nil
}
