package tls

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"regexp"
	"strings"

	"github.com/borderzero/border0-cli/internal/client"
	"github.com/borderzero/border0-cli/internal/enum"
	"github.com/spf13/cobra"
)

var (
	hostname string
	listener int
)

// clientTlsCmd represents the client tls command
var clientTlsCmd = &cobra.Command{
	Use:               "tls",
	Short:             "Connect to a border0 TLS protected socket",
	ValidArgsFunction: client.AutocompleteHost,
	RunE: func(cmd *cobra.Command, args []string) error {
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
		substr := "(.*).border0-dummy$"
		r, _ := regexp.Compile(substr)
		match := r.FindStringSubmatch(hostname)
		if match != nil {
			hostname = match[1]
		}

		info, err := client.GetResourceInfo(hostname)
		if err != nil {
			log.Fatalf("failed to get certificate: %v", err.Error())
		}

		certificate := tls.Certificate{
			Certificate: [][]byte{info.Certficate.Raw},
			PrivateKey:  info.PrivateKey,
		}

		tlsConfig := tls.Config{
			Certificates:       []tls.Certificate{certificate},
			InsecureSkipVerify: true,
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
					var conn net.Conn
					if info.ConnectorAuthenticationEnabled {
						conn, err = client.ConnectorAuthConnect(fmt.Sprintf("%s:%d", hostname, info.Port), &tlsConfig)
						if err != nil {
							log.Printf("failed to connect: %v", err.Error())
							return
						}
					} else {
						conn, err = tls.Dial("tcp", fmt.Sprintf("%s:%d", hostname, info.Port), &tlsConfig)
						if err != nil {
							log.Printf("failed to connect: %v", err.Error())
							return
						}

					}

					log.Print("Connection established from ", lcon.RemoteAddr())
					tcp_con_handle(conn, lcon, lcon)
				}()
			}
		} else {
			var conn net.Conn
			if info.ConnectorAuthenticationEnabled {
				conn, err = client.ConnectorAuthConnect(fmt.Sprintf("%s:%d", hostname, info.Port), &tlsConfig)
				if err != nil {
					log.Fatalf("failed to connect: %v", err.Error())
				}
			} else {
				conn, err = tls.Dial("tcp", fmt.Sprintf("%s:%d", hostname, info.Port), &tlsConfig)
				if err != nil {
					log.Fatalf("failed to connect: %v", err.Error())
				}
			}
			tcp_con_handle(conn, os.Stdin, os.Stdout)
		}

		return err
	},
}

func tcp_con_handle(con net.Conn, in io.Reader, out io.Writer) {
	chan_to_stdout := stream_copy(con, out)
	chan_to_remote := stream_copy(in, con)

	select {
	case <-chan_to_stdout:
	case <-chan_to_remote:
	}
}

// Performs copy operation between streams: os and tcp streams
func stream_copy(src io.Reader, dst io.Writer) <-chan int {
	buf := make([]byte, 1024)
	sync_channel := make(chan int)
	go func() {
		defer func() {
			if con, ok := dst.(net.Conn); ok {
				con.Close()
			}
			sync_channel <- 0 // Notify that processing is finished
		}()
		for {
			var nBytes int
			var err error
			nBytes, err = src.Read(buf)
			if err != nil {
				if err != io.EOF && !strings.Contains(err.Error(), "use of closed network connection") {
					log.Printf("Read error: %s\n", err)
				}
				break
			}
			_, err = dst.Write(buf[0:nBytes])
			if err != nil {
				log.Fatalf("Write error: %s\n", err)
			}
		}
	}()
	return sync_channel
}

func AddCommandsTo(client *cobra.Command) {
	client.AddCommand(clientTlsCmd)

	clientTlsCmd.Flags().StringVarP(&hostname, "host", "", "", "The border0 target host")
	clientTlsCmd.Flags().IntVarP(&listener, "listener", "l", 0, "Listener port number")
}
