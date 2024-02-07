package utils

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/borderzero/border0-cli/cmd/logger"
	"github.com/borderzero/border0-cli/internal/client"
	"github.com/skratchdot/open-golang/open"
	"github.com/spf13/cobra"
)

func openRDP(address string) error {
	rdpFileContents := []byte(fmt.Sprintf("full address:s:%s\nprompt for credentials:i:1", address))

	// Create temporary .rdp file
	tmpDir := os.TempDir()
	defer os.RemoveAll(tmpDir)

	rdpFilePath := filepath.Join(tmpDir, "temp.rdp")
	if err := os.WriteFile(rdpFilePath, rdpFileContents, 0644); err != nil {
		return fmt.Errorf("failed to create RDP file: %w", err)
	}

	// We open the client twice... because
	// Microsoft's Remote Desktop client refuses
	// to configure a new machine if the app is not
	// already open.
	open.Run(rdpFilePath)
	time.Sleep(time.Second * 1)
	return open.Run(rdpFilePath)
}

// StartLocalProxyAndOpenClient starts a local listener on the given
// local port and opens the system's default client application
// for specified protocol.
func StartLocalProxyAndOpenClient(
	cmd *cobra.Command,
	args []string,
	protocol string,
	hostname string,
	localListenerPort int,
) error {
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

	localListenerAddress := fmt.Sprintf("localhost:%d", localListenerPort)

	l, err := net.Listen("tcp", localListenerAddress)
	if err != nil {
		log.Fatalln("Error: Unable to start local TLS listener.")
	}
	log.Print("Waiting for connections...")

	go func() {
		var err error
		if protocol == "rdp" {
			err = openRDP(localListenerAddress)
		} else {
			err = open.Run(fmt.Sprintf("%s://%s", protocol, localListenerAddress))
		}
		if err != nil {
			log.Print(fmt.Sprintf("Failed to open system's %s client: %v", protocol, err))
		}
	}()

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
			Copy(conn, lcon)
		}()
	}
}
