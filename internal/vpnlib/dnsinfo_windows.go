//go:build windows
// +build windows

package vpnlib

import (
	"fmt"
	"runtime"

	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// GetDnsServers returns a list of all active resolvers used by the system
func GetDnsServers() ([]string, error) {
	switch runtime.GOOS {
	case "windows":
		return getDnsWindows()
	default:
		return nil, fmt.Errorf("runtime %s not supported", runtime.GOOS)
	}
}

func getDnsWindows() ([]string, error) {
	interfaces, err := winipcfg.GetAdaptersAddresses(2, winipcfg.GAAFlagDefault)
	if err != nil {
		fmt.Printf("Error getting adapters: %v\n", err)
		return nil, err
	}

	var resolvers []string
	// Iterate over each adapter
	for _, adapter := range interfaces {
		if adapter.OperStatus != winipcfg.IfOperStatusUp {
			continue
		}
		// Iterate over DNS servers for the current adapter
		for dnsServer := adapter.FirstDNSServerAddress; dnsServer != nil; dnsServer = dnsServer.Next {
			// add the IP address of the DNS server to the map
			ip := dnsServer.Address.IP()
			resolvers = append(resolvers, ip.String())
		}
	}

	return resolvers, nil
}
