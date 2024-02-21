package vpn

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/borderzero/border0-cli/cmd/logger"
	"github.com/borderzero/border0-cli/internal/client"
	"github.com/borderzero/border0-cli/internal/enum"
	"github.com/borderzero/border0-cli/internal/vpnlib"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

var (
	hostname string
)

type networkRoute struct {
	network   string
	nextHopIp string
}

// clientVpnCmd represents the client tls command
var clientVpnCmd = &cobra.Command{
	Use:               "vpn",
	Short:             "Start a Border0 VPN",
	ValidArgsFunction: client.AutocompleteHost,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			hostname = args[0]
		}

		if hostname == "" {
			pickedHost, err := client.PickHost(hostname, enum.TLSSocket)
			if err != nil {
				return fmt.Errorf("failed to pick host: %v", err)
			}
			hostname = pickedHost.Hostname()
		}

		info, err := client.GetResourceInfo(logger.Logger, hostname)
		if err != nil {
			return fmt.Errorf("failed to get certificate: %v", err)
		}

		certificate := tls.Certificate{
			Certificate: [][]byte{info.Certficate.Raw},
			PrivateKey:  info.PrivateKey,
		}

		systemCertPool, err := x509.SystemCertPool()
		if err != nil {
			log.Fatalf("failed to get system cert pool: %v", err.Error())
		}

		tlsConfig := tls.Config{
			Certificates: []tls.Certificate{certificate},
			RootCAs:      systemCertPool,
			ServerName:   hostname,
		}

		conn, err := establishConnection(info.ConnectorAuthenticationEnabled, info.EndToEndEncryptionEnabled, fmt.Sprintf("%s:%d", hostname, info.Port), &tlsConfig, info.CaCertificate)
		if err != nil {
			return fmt.Errorf("failed to connect: %v", err)
		}

		defer conn.Close()

		iface, err := vpnlib.CreateTun()
		if err != nil {
			return fmt.Errorf("failed to create TUN interface: %v", err)
		}

		logger.Logger.Info("Created TUN interface", zap.String("interface_name", iface.Name()))

		defer iface.Close()

		ctrl, err := vpnlib.GetControlMessage(conn)
		if err != nil {
			return fmt.Errorf("failed to get control message from connection: %v\nIt's likely the Remote VPN server hasnt been started", err)
		}
		logger.Logger.Info("Connected, Session info:", zap.Any("control_message", ctrl))

		if err = vpnlib.AddIpToIface(iface.Name(), ctrl.ClientIp, ctrl.ServerIp, ctrl.SubnetSize); err != nil {
			log.Println("failed to add IPs to interface", err)
		}

		// Create a channel to receive OS signals.
		sigCh := make(chan os.Signal, 1)

		// Notify the `sigCh` channel for SIGINT (Ctrl+C) signals.
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)

		// keep track of the routes we need to delete when we exit
		routesToDel := []networkRoute{}

		// we should also add a defer, so we clean this up when we exit
		defer cleanUpAfterSessionDown(routesToDel)

		// Get the remote address of the tunnel connection
		// we'll use that to determine the address family
		remoteAddr := conn.RemoteAddr().(*net.TCPAddr)

		var addressFamily int // Tunnel over IPv4 or IPv6
		var vpnGatewayIp string

		if remoteAddr.IP.To4() != nil {
			addressFamily = 4
			vpnGatewayIp = remoteAddr.IP.String() + "/32"
		} else if remoteAddr.IP.To16() != nil {
			addressFamily = 6
			vpnGatewayIp = remoteAddr.IP.String() + "/128"
		} else {
			return fmt.Errorf("failed to determine address family for remote address: %v", remoteAddr.IP)
		}

		// rewrite default route to 0.0.0.0/1 and 128.0.0.1
		for i, route := range ctrl.Routes {

			if route == "0.0.0.0/0" {

				// Before we add new routes, we need to add a more specific route to the gateway gatewayIp
				// This is because we want the VPN gateway IP to be routed through the existing gateway
				// If we don't do this, the VPN gateway IP will be routed through the VPN, which will cause a loop

				// The IP of the VPN gateway the the remote end of conn.
				// So let's get the IP of the remote end of conn and then turn that into a /32 route
				// and route it via the old gateway

				// then delete this route (0.0.0.0/0) from the list of routes
				ctrl.Routes = append(ctrl.Routes[:i], ctrl.Routes[i+1:]...)

				// and add two new more specific routes
				if route == "0.0.0.0/0" {
					ctrl.Routes = append(ctrl.Routes, "0.0.0.0/1", "128.0.0.0/1")
				} else if route == "::/0" {
					ctrl.Routes = append(ctrl.Routes, "200::/3")
				}

				if addressFamily == 4 {
					// get the existing default route, so we can override it
					// This will return both the gateway IP and the interface name
					LocalGatewayIp, _, err := vpnlib.GetDefaultGateway(4)
					if err != nil {
						return fmt.Errorf("failed to get default route: %v", err)
					}

					if err = vpnlib.AddRoutesViaGateway(LocalGatewayIp.String(), []string{vpnGatewayIp}); err != nil {
						log.Fatalf("failed to add static route for VPN Gateway %s, towards %s: %v\n", vpnGatewayIp, LocalGatewayIp.String(), err)
					}

					// add the newly create static route for VPN gateway to the list of routes to delete
					routesToDel = append(routesToDel, networkRoute{network: vpnGatewayIp, nextHopIp: LocalGatewayIp.String()})

				} else if addressFamily == 6 {
					// TODO placeholder for ipv6
					// for now we don't support bypass routes for ipv6
					// once we start announcing ipv6 routes, we can should add support for bypass routes
					// for now we just skip this
				}
			}
		}

		// Check if we need to add bypass routes for the DNS servers
		// Needed when the DNS server is RFC1918 and is on a different subnet than the default gateway
		// This is the case in many coffee shops, where the DNS server is on a different subnet than the default gateway
		// In this case, we need to add a bypass route for the DNS server, so that it's not routed through the VPN
		if addressFamily == 4 {
			// dnsServersBypassRoutes is a list of DNS servers that we need to add bypass routes for
			dnsServersBypassRoutes, err := vpnlib.GetDnsByPassRoutes(iface.Name(), ctrl.Routes, addressFamily)
			if err != nil {
				log.Println("failed to get DNS servers bypass routes", err)
			}
			if len(dnsServersBypassRoutes) > 0 {
				// Now we can add the bypass routes for the DNS servers
				LocalGatewayIp, _, err := vpnlib.GetDefaultGateway(4)
				if err != nil {
					return fmt.Errorf("failed to get default route: %v", err)
				}

				for dnsServer := range dnsServersBypassRoutes {
					fmt.Println("Adding bypass route for DNS server", dnsServer, "via", LocalGatewayIp.String())
					err = vpnlib.AddRoutesViaGateway(LocalGatewayIp.String(), []string{dnsServer})
					if err != nil {
						log.Println("failed to add bypass route for DNS server", dnsServer, "via", LocalGatewayIp.String(), err)
					}
					// add the newly create static route for DNS server to the list of routes to delete
					routesToDel = append(routesToDel, networkRoute{network: dnsServer, nextHopIp: LocalGatewayIp.String()})
				}
			}
		}

		// Now we can add the routes that the server sent to us a the client
		// These routes will be routed through the VPN

		// check if we're running on Windows , if so we need to sleep for a few seconds
		// Seems like it's slow to update the routing table on Windows
		// if we don't wait, windows will use the wrong "Interface" for the route.
		// It seems to then choose the old default gateway, which is not what we want
		// So we wait a few seconds, and then add the routes, it then picks the VPN interfaces as the correct interface
		if runtime.GOOS == "windows" {
			fmt.Printf("Adding VPN routes, waiting for interface to be ready...")
			// for loop with one 500ms sleep each
			for i := 0; i < 10; i++ {
				time.Sleep(500 * time.Millisecond)
				fmt.Print(".")
			}
			fmt.Println()
		}
		if err = vpnlib.AddRoutesViaGateway(ctrl.ServerIp, ctrl.Routes); err != nil {
			log.Println("failed to add routes to interface", err)
		}

		// create the connection map
		cm := vpnlib.NewConnectionMap()
		cm.Set(ctrl.ServerIp, conn)
		defer cm.Delete(ctrl.ServerIp)

		fmt.Println("Connected to", conn.RemoteAddr())

		// Start a goroutine to handle OS signals and make sure we clean up when we exit
		go func() {
			<-sigCh
			cleanUpAfterSessionDown(routesToDel)
			os.Exit(0)

		}()

		// Now start the Tun to Conn goroutine
		// This will listen for packets on the TUN interface and forward them to the right connection
		go vpnlib.TunToConnCopy(iface, cm, true, conn)

		// Let's start a goroutine that send keep alive ping messges to the other side
		go func() {
			// Create ICMP Echo message
			c, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
			if err != nil {
				fmt.Println("Error opening ICMP socket:", err)
				return
			}
			defer c.Close()
			msg := icmp.Message{
				Type: ipv4.ICMPTypeEcho, Code: 0,
				Body: &icmp.Echo{
					ID: os.Getpid() & 0xffff, Seq: 1,
					Data: []byte("Border0-keepalive-ping"),
				},
			}
			// Convert message to bytes
			b, err := msg.Marshal(nil)
			if err != nil {
				fmt.Println("Error marshaling ICMP message:", err)
				return
			}

			for {
				// send an ICMP ping to ctrl.ServerIp
				n, err := c.WriteTo(b, &net.IPAddr{IP: net.ParseIP(ctrl.ServerIp)})
				if err != nil {
					fmt.Println("Error writing ICMP keep alive message:", err)
					return
				} else if n != len(b) {
					fmt.Println("Got short write from WriteTo")
					return
				}
				time.Sleep(120 * time.Second)
			}
		}()

		// Now start the Conn to Tun goroutine
		// This will listen for packets on the connection and forward them to the TUN interface
		// note: this blocks
		fmt.Println("Forwarding packets, from conn:", conn, "to TUN iface:", iface)
		if err = vpnlib.ConnToTunCopy(conn, iface); err != nil {
			fmt.Println("Error forwarding packets:", err)
			return fmt.Errorf("failed to forward between tls conn and TUN iface: %v", err)
		}

		fmt.Println("Done forwarding packets")
		return nil
	},
}

func cleanUpAfterSessionDown(routesToDelete []networkRoute) {
	for _, route := range routesToDelete {
		err := vpnlib.DeleteRoutesViaGateway(route.nextHopIp, []string{route.network})
		if err != nil {
			log.Println("failed to delete static route during session clean up", route.network, "via", route.nextHopIp, err)
		}
	}
}

func establishConnection(connectorAuthenticationEnabled, end2EndEncryptionEnabled bool, addr string, tlsConfig *tls.Config, caCertificate *x509.Certificate) (conn net.Conn, err error) {
	if connectorAuthenticationEnabled || end2EndEncryptionEnabled {
		conn, err = client.Connect(addr, tlsConfig, tlsConfig.Certificates[0], caCertificate, connectorAuthenticationEnabled, end2EndEncryptionEnabled)
	} else {
		conn, err = tls.Dial("tcp", addr, tlsConfig)
	}
	return
}

func AddCommandsTo(client *cobra.Command) {
	client.AddCommand(clientVpnCmd)

	clientVpnCmd.Flags().StringVarP(&hostname, "service", "", "", "The Border0 service identifier")
}
