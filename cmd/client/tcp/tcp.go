package tcp

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"os"
	"regexp"

	"github.com/borderzero/border0-cli/cmd/client/utils"
	"github.com/borderzero/border0-cli/cmd/logger"
	"github.com/borderzero/border0-cli/internal/client"
	"github.com/borderzero/border0-cli/internal/enum"
	"github.com/spf13/cobra"
)

var (
	hostname string
	listener int
)

func tcpHandler(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		hostname = args[0]
	}

	if hostname == "" {
		pickedHost, err := client.PickHost(hostname, enum.TLSSocket)
		if err != nil {
			return err
		}
		hostname = pickedHost.Hostname()
	}

	//Check for  hostname checking in *.border0-dummy
	// This may be used by ssh users
	// if so strip that
	substr := "(.*)\\.border0\\-dummy$"
	r, _ := regexp.Compile(substr)
	match := r.FindStringSubmatch(hostname)
	if match != nil {
		hostname = match[1]
	}

	info, err := client.GetResourceInfo(logger.Logger, hostname)
	if err != nil {
		log.Fatalf("failed to get certificate: %v", err.Error())
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

	if listener > 0 {
		l, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", listener))
		if err != nil {
			log.Fatalln("Error: Unable to start local TLS listener.")
		}

		log.Print("Waiting for connections...")

		for {
			lcon, err := l.Accept()
			if err != nil {
				log.Fatalf("Listener: Accept Error: %s\n", err)
			}

			go func() {
				conn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", hostname, info.Port), &tlsConfig)
				if err != nil {
					fmt.Printf("failed to connect to %s:%d: %s\n", hostname, info.Port, err)
				}

				if info.ConnectorAuthenticationEnabled || info.EndToEndEncryptionEnabled {
					conn, err = client.ConnectWithConn(conn, certificate, info.CaCertificate, info.ConnectorAuthenticationEnabled, info.EndToEndEncryptionEnabled)
					if err != nil {
						fmt.Printf("failed to connect: %s\n", err)
					}
				}

				log.Print("Connection established from ", lcon.RemoteAddr())
				utils.Copy(conn, lcon, lcon)
			}()
		}
	} else {
		conn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", hostname, info.Port), &tlsConfig)
		if err != nil {
			return fmt.Errorf("failed to connect to %s:%d: %w", hostname, info.Port, err)
		}

		if info.ConnectorAuthenticationEnabled || info.EndToEndEncryptionEnabled {
			conn, err = client.ConnectWithConn(conn, certificate, info.CaCertificate, info.ConnectorAuthenticationEnabled, info.EndToEndEncryptionEnabled)
			if err != nil {
				return fmt.Errorf("failed to connect: %w", err)
			}
		}

		utils.Copy(conn, os.Stdin, os.Stdout)
	}

	return err

}

// clientTcpCmd represents the client tcp command
var clientTcpCmd = &cobra.Command{
	Use:               "tcp",
	Short:             "Connect to a border0 TCP socket",
	ValidArgsFunction: client.AutocompleteHost,
	RunE:              tcpHandler,
}

// clientTlsCmd represents the client tls command
var clientTlsCmd = &cobra.Command{
	Use:               "tls",
	Short:             "Connect to a border0 TCP socket",
	ValidArgsFunction: client.AutocompleteHost,
	RunE:              tcpHandler,
}

func AddCommandsTo(client *cobra.Command) {
	clientTcpCmd.Flags().StringVarP(&hostname, "host", "", "", "The border0 target host")
	clientTcpCmd.Flags().IntVarP(&listener, "listener", "l", 0, "Listener port number")
	client.AddCommand(clientTcpCmd)

	clientTlsCmd.Flags().StringVarP(&hostname, "host", "", "", "The border0 target host")
	clientTlsCmd.Flags().IntVarP(&listener, "listener", "l", 0, "Listener port number")
	clientTlsCmd.Hidden = true
	client.AddCommand(clientTlsCmd)
}
