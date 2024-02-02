package vpn

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/borderzero/border0-cli/cmd/logger"
	"github.com/borderzero/border0-cli/internal/client"
	"github.com/borderzero/border0-cli/internal/enum"
	"github.com/borderzero/border0-cli/internal/vpnlib"
	"github.com/labulakalia/water"
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

		iface, err := water.New(water.Config{DeviceType: water.TUN})
		if err != nil {
			return fmt.Errorf("failed to create TUN iface: %v", err)
		}
		logger.Logger.Info("Created TUN interface", zap.String("interface_name", iface.Name()))

		defer iface.Close()

		ctrl, err := vpnlib.GetControlMessage(conn)
		if err != nil {
			return fmt.Errorf("failed to get control message from connection: %v\nIt's likely the Remote VPN server hasnt been started", err)
		}
		logger.Logger.Info("Received control message", zap.Any("control_message", ctrl))

		if err = vpnlib.AddIpToIface(iface.Name(), ctrl.ClientIp, ctrl.ServerIp, ctrl.SubnetSize); err != nil {
			log.Println("failed to add IPs to interface", err)
		}

		// Create a channel to receive OS signals.
		sigCh := make(chan os.Signal, 1)

		// Notify the `sigCh` channel for SIGINT (Ctrl+C) signals.
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)

		// keep track of the routes we need to delete when we exit
		routesToDel := []networkRoute{}

		// rewrite default route to 0.0.0.0/1 and 128.0.0.1
		for i, route := range ctrl.Routes {
			if route == "0.0.0.0/0" {

				// get the existing default route, so we can override it
				// This will return both the gateway IP and the interface name

				LocalGatewayIp, gatewayInterface, err := vpnlib.GetDefaultGateway()
				if err != nil {
					return fmt.Errorf("failed to get default route: %v", err)
				}
				// Before we add new routes, we need to add a more specific route to the gateway gatewayIp
				// This is because we want the VPN gateway IP to be routed through the existing gateway
				// If we don't do this, the VPN gateway IP will be routed through the VPN, which will cause a loop

				// The IP of the VPN gateway the the remote end of conn.
				// So let's get the IP of the remote end of conn and then turn that into a /32 route
				// and route it via the old gateway
				vpnGatewayIp := conn.RemoteAddr().(*net.TCPAddr).IP.String() + "/32"

				if err = vpnlib.AddRoutesViaGateway(LocalGatewayIp.String(), []string{vpnGatewayIp}, iface.Name()); err != nil {
					log.Fatalf("failed to add static route for VPN Gateway %s, towards %s (%s): %v\n", vpnGatewayIp, gatewayInterface, LocalGatewayIp.String(), err)
				}

				// add the newly create static route for VPN gateway to the list of routes to delete
				routesToDel = append(routesToDel, networkRoute{network: vpnGatewayIp, nextHopIp: LocalGatewayIp.String()})

				// then delete this route
				ctrl.Routes = append(ctrl.Routes[:i], ctrl.Routes[i+1:]...)
				// and add two new routes
				ctrl.Routes = append(ctrl.Routes, "0.0.0.0/1", "128.0.0.0/1")

				// Also get the DNS servers from the server and add them to the map
				// so we can use them later
				currentDnsServers, err := vpnlib.GetDnsServers()
				if err != nil {
					log.Println("failed to get current DNS servers", err)
				}

				for _, dnsServer := range currentDnsServers {
					// Check if this is a local IP, or routed via default gateway
					// If so, we don't want to add it to the static routes

					networkInterfaces, err := vpnlib.GetLocalInterfacesForIp(dnsServer)
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
						vpnIfaceName := iface.Name()
						for _, name := range networkInterfaces {
							if name != vpnIfaceName {
								// network found, not adding bypass route
								continue
							}

						}
					}

					// For now we old support IPv4
					// Make sure that DNS servers only has IPv4 addresses.
					if net.ParseIP(dnsServer).To4() != nil {
						// Make sure we add a bypass route for the DNS servers
						// so we don't route them through the VPN
						err = vpnlib.AddRoutesViaGateway(LocalGatewayIp.String(), []string{dnsServer}, iface.Name())
						if err != nil {
							log.Println("failed to add static route for DNS server", err)
						}
						// Also make sure we clean this up later, by adding it to the list of routes to delete
						routesToDel = append(routesToDel, networkRoute{network: dnsServer, nextHopIp: LocalGatewayIp.String()})
					}
				}

				// we should also add a defer, so we clean this up when we exit
				defer cleanUpAfterSessionDown(routesToDel)
				break

			}
		}

		// Now we can add the routes that the server sent to us a the client
		// These routes will be routed through the VPN

		// if err = vpnlib.AddRoutesToIface(iface.Name(), ctrl.Routes); err != nil {
		// 	log.Println("failed to add routes to interface", err)
		// }
		if err = vpnlib.AddRoutesViaGateway(ctrl.ServerIp, ctrl.Routes, iface.Name()); err != nil {
			log.Println("failed to add routes to interface", err)
		}

		// create the connection map
		cm := vpnlib.NewConnectionMap()
		cm.Set(ctrl.ServerIp, conn)
		defer cm.Delete(ctrl.ServerIp)

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
