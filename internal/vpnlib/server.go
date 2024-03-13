package vpnlib

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"runtime"

	"github.com/borderzero/border0-cli/cmd/logger"
	"go.uber.org/zap"
)

// RunServer runs the VPN "server"
func RunServer(
	ctx context.Context,
	vpnClientListener net.Listener,
	dhcpPoolSubnet string,
	advertisedRoutes []string,
	middlewares ...PacketMiddleware,
) error {
	// Create an IP pool that will be used to assign IPs to clients
	dhcpPool, err := NewIPPool(dhcpPoolSubnet)
	if err != nil {
		log.Fatalf("Failed to create IP Pool: %v", err)
	}
	subnetSize := dhcpPool.GetSubnetSize()
	serverIp := dhcpPool.GetServerIp()

	tun, err := CreateTun()
	if err != nil {
		return fmt.Errorf("failed to create TUN interface: %v", err)
	}
	defer tun.Close()

	logger.Logger.Info("Started VPN server", zap.String("interface", tun.Name()), zap.String("server_ip", serverIp), zap.String("dhcp_pool_subnet ", dhcpPoolSubnet))

	if err = AddServerIp(tun.Name(), serverIp, subnetSize); err != nil {
		return fmt.Errorf("failed to add server IP to interface: %v", err)
	}

	if runtime.GOOS != "linux" {
		// On linux the routes are added to the interface when creating the interface and adding the IP
		if err = AddRoutesToIface(tun.Name(), []string{dhcpPoolSubnet}); err != nil {
			logger.Logger.Warn("failed to add routes to interface", zap.Error(err))
		}
	}

	if runtime.GOOS == "linux" {
		// Check if ip forwarding is enabled
		forwardingEnabled, err := CheckIPForwardingEnabled()
		if err != nil {
			logger.Logger.Warn("Failed to check if ip forwarding is enabled", zap.Error(err))
		}
		if !forwardingEnabled {
			logger.Logger.Warn("Ip forwarding is not enabled, Your VPN will not be able to forward packets")
			logger.Logger.Warn("To enable ip forwarding run: sysctl -w net.ipv4.ip_forward=1")
			logger.Logger.Warn("Also make sure to enable NAT: iptables -t nat -A POSTROUTING -o <interface> -j MASQUERADE")
		}
	}

	// create the connection map
	connMap := NewConnectionMap()

	// Now start the Tun to Conn goroutine
	// This will listen for packets on the TUN interface and forward them to the right connection
	go TunToConnMapCopy(ctx, tun, connMap)

	for {
		select {
		case <-ctx.Done():
			if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
			return nil
		default:
			client, err := vpnClientListener.Accept()
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					fmt.Printf("failed to accept new vpn connection: %v\n", err)
				}
				continue // note: if context is cancelled, above case will catch it
			}
			go handleIPPacketConn(
				ctx,
				client,
				tun,
				dhcpPool,
				connMap,
				advertisedRoutes,
				middlewares...,
			)
		}
	}
}

func handleIPPacketConn(
	ctx context.Context,
	client net.Conn,
	tun io.Writer,
	dhcpPool *IPPool,
	connMap *ConnectionMap,
	advertisedRoutes []string,
	middlewares ...PacketMiddleware,
) {
	defer client.Close()

	// allocate a new IP in the pool for the new client
	clientIP, err := dhcpPool.Allocate()
	if err != nil {
		fmt.Printf("failed to allocate client IP: %v\n", err)
		return
	}
	defer dhcpPool.Release(clientIP)

	// attach new client connection to connection map
	connMap.Set(clientIP, client)
	defer connMap.Delete(clientIP)

	fmt.Printf("new client connected allocated IP: %s\n", clientIP)

	// define control message
	controlMessage := &ControlMessage{
		ClientIp:   clientIP,
		ServerIp:   dhcpPool.GetServerIp(),
		SubnetSize: dhcpPool.GetSubnetSize(),
		Routes:     advertisedRoutes,
	}
	controlMessageBytes, err := controlMessage.Build()
	if err != nil {
		fmt.Printf("failed to build control message: %v\n", err)
		return
	}

	// write control message
	n, err := client.Write(controlMessageBytes)
	if err != nil {
		fmt.Printf("failed to write control message to net conn: %v\n", err)
		return
	}
	if n < len(controlMessageBytes) {
		fmt.Printf("failed to write entire control message bytes (is %d, wrote %d)\n", controlMessageBytes, n)
		return
	}

	// kick off routine to read packets from clients and forward them to the interface
	if err = ConnToTunCopy(ctx, client, tun, middlewares...); err != nil {
		if !errors.Is(err, io.EOF) {
			fmt.Printf("failed to forward packets between client conn and interface: %v\n", err)
		}
		return
	}
}
