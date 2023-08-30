package vpnlib

import (
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"

	"github.com/borderzero/border0-cli/cmd/logger"
	"github.com/songgao/water"
	"go.uber.org/zap"
)

func RunVpn(l net.Listener, vpnSubnet string, routes []string) error {
	// Create an IP pool that will be used to assign IPs to clients
	ipPool, err := NewIPPool(vpnSubnet)
	if err != nil {
		return fmt.Errorf("Failed to create IP Pool: %v", err)
	}
	subnetSize := ipPool.GetSubnetSize()
	serverIp := ipPool.GetServerIp()

	// create the connection map
	cm := NewConnectionMap()

	iface, err := water.New(water.Config{DeviceType: water.TUN})
	if err != nil {
		return fmt.Errorf("failed to create TUN iface: %v", err)
	}
	defer iface.Close()
	logger.Logger.Info("Started VPN server", zap.String("interface", iface.Name()), zap.String("server_ip", serverIp), zap.String(" vpn_subnet ", vpnSubnet))

	if err = AddServerIp(iface.Name(), serverIp, subnetSize); err != nil {
		return fmt.Errorf("failed to add server IP to interface: %v", err)
	}

	if runtime.GOOS != "linux" {
		// On linux the routes are added to the interface when creating the interface and adding the IP
		if err = AddRoutesToIface(iface.Name(), []string{vpnSubnet}); err != nil {
			logger.Logger.Warn("failed to add routes to interface", zap.Error(err))
		}
	}

	// Now start the Tun to Conn goroutine
	// This will listen for packets on the TUN interface and forward them to the right connection
	go TunToConnCopy(iface, cm, false, nil)

	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Printf("Failed to accept new vpn connection: %v\n", err)
			continue
		}

		// dispatch new connection handler
		go func() {
			defer conn.Close()

			// get an ip for the new client
			clientIp, err := ipPool.Allocate()
			if err != nil {
				fmt.Printf("Failed to allocate client IP: %v\n", err)
				return
			}
			defer ipPool.Release(clientIp)

			fmt.Printf("New client connected allocated IP: %s\n", clientIp)

			// attach the connection to the client ip
			cm.Set(clientIp, conn)
			defer cm.Delete(clientIp)

			// define control message
			controlMessage := &ControlMessage{
				ClientIp:   clientIp,
				ServerIp:   serverIp,
				SubnetSize: uint8(subnetSize),
				Routes:     routes,
			}
			controlMessageBytes, err := controlMessage.Build()
			if err != nil {
				fmt.Printf("failed to build control message: %v\n", err)
				return
			}

			// write configuration message
			n, err := conn.Write(controlMessageBytes)
			if err != nil {
				fmt.Printf("failed to write control message to net conn: %v\n", err)
				return
			}
			if n < len(controlMessageBytes) {
				fmt.Printf("failed to write entire control message bytes (is %d, wrote %d)\n", controlMessageBytes, n)
				return
			}

			if err = ConnToTunCopy(conn, iface); err != nil {
				if !errors.Is(err, io.EOF) {
					fmt.Printf("failed to forward between tls conn and TUN iface: %v\n", err)
				}
				return
			}
		}()
	}
}
