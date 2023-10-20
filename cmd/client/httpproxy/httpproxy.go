package httpproxy

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	b0tls "github.com/borderzero/border0-cli/cmd/client/tls"
	"github.com/borderzero/border0-cli/cmd/logger"
	"github.com/borderzero/border0-cli/internal/client"
	"github.com/borderzero/border0-cli/internal/enum"
	"github.com/hashicorp/yamux"
	"github.com/spf13/cobra"
)

var (
	hostname      string
	httpProxyPort int
	numStreams    int
)

type SessionManager struct {
	sessions      []*yamux.Session
	loggingStream []*yamux.Stream
	info          *client.ResourceInfo
	tlsConfig     *tls.Config
	hostname      string
	sessionsMutex sync.Mutex
}

// clientProxyCmd represents the client tls command
var clientProxyCmd = &cobra.Command{
	Use:               "proxy",
	Short:             "Connect a Border0 HTTP proxy",
	ValidArgsFunction: client.AutocompleteHost,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			hostname = args[0]
		}

		if numStreams < 1 {
			return fmt.Errorf("number of streams must be greater than 0")
		} else if numStreams > 10 {
			return fmt.Errorf("number of streams must be less than 10")
		}

		if httpProxyPort == 0 {
			return fmt.Errorf("port number is required")
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

		tlsConfig := tls.Config{
			Certificates:       []tls.Certificate{certificate},
			InsecureSkipVerify: true,
		}

		// Create a multiplexed session over multiple TCP connections
		//sessions := make([]*yamux.Session, numStreams)
		sessionManager := &SessionManager{}

		for i := 0; i < numStreams; i++ {

			_, session, logStream, err := sessionManager.createSession(&info, &tlsConfig, hostname)
			if err != nil {
				log.Fatalf("Failed to create upstream session: %v", err)
			}
			log.Printf("Upstream connection %d => Connected to %s:%d\n", i, hostname, info.Port)

			sessionManager.addSession(session, logStream, &info, &tlsConfig, hostname)
		}

		// Now start the local listener on which we accept the traffic
		l, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", httpProxyPort))
		if err != nil {
			log.Fatalf("error: Unable to start tcp listener on port %d, %s\n", httpProxyPort, err)
		}
		defer l.Close()
		log.Printf("service started, listening for connections on port %d\n", httpProxyPort)

		sessionIndex := 0

		for {
			clientConn, err := l.Accept()
			if err != nil {
				log.Printf("Failed to accept connection: %v", err)
				continue
			}

			selectedSession := sessionManager.selectSession(sessionIndex)

			// Increment the index so that next time a different session is used
			sessionIndex++

			go handleClientConnection(clientConn, selectedSession)
		}
	},
}

// SessionManager is a simple struct that holds a slice of yamux sessions
// here we can add and remove sessions as they are created and closed
func (sm *SessionManager) addSession(session *yamux.Session, logStream *yamux.Stream, info *client.ResourceInfo, tlsConfig *tls.Config, hostname string) {
	sm.sessionsMutex.Lock()
	defer sm.sessionsMutex.Unlock()
	sm.sessions = append(sm.sessions, session)
	sm.loggingStream = append(sm.loggingStream, logStream)
	sm.info = info
	sm.tlsConfig = tlsConfig
	sm.hostname = hostname

	go func() {
		<-session.CloseChan() // Wait for the session to close
		sm.removeSession(session, logStream)
	}()
}

func (sm *SessionManager) createSession(info *client.ResourceInfo, tlsConfig *tls.Config, hostname string) (net.Conn, *yamux.Session, *yamux.Stream, error) {
	conn, err := b0tls.EstablishConnection(info.ConnectorAuthenticationEnabled, info.EndToEndEncryptionEnabled, fmt.Sprintf("%s:%d", hostname, info.Port), tlsConfig)
	if err != nil {
		return nil, nil, nil, err
	}

	session, err := yamux.Client(conn, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	// Accept the first stream for logging
	logStream, err := session.AcceptStream()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to accept logging stream: %v", err)
	}

	// Read log messages from the stream in a separate goroutine

	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := logStream.Read(buf)
			if err != nil {
				if err != io.EOF {
					log.Printf("Failed to read log message: %v", err)
				}
				break
			}
			logLine := string(buf[:n])
			log.Println(logLine)
		}
	}()
	return conn, session, logStream, nil
}

// removeSession is a simple function that removes a session from the slice
func (sm *SessionManager) removeSession(session *yamux.Session, logStream *yamux.Stream) {
	sm.sessionsMutex.Lock()
	defer sm.sessionsMutex.Unlock()
	for i, s := range sm.sessions {
		if s == session {
			sm.sessions = append(sm.sessions[:i], sm.sessions[i+1:]...)
			sm.loggingStream = append(sm.loggingStream[:i], sm.loggingStream[i+1:]...) // Remove the corresponding logging stream

			log.Printf("one of the upstream connections went down and has been removed. Pool size is now %d\n", len(sm.sessions))
			log.Printf("attempting to reconnect to %s:%d\n", sm.hostname, sm.info.Port)

			// Reconnect logic
			_, session, logStream, err := sm.createSession(sm.info, sm.tlsConfig, sm.hostname)
			if err != nil {
				log.Printf("Failed to create stream session: %v\n", err)
				if len(sm.sessions) == 0 {
					log.Fatalf("All upstream sessions have been closed, no more upstream options available. Exiting.")
				}
				return
			}
			// Add the new session back to the pool
			sm.addSession(session, logStream, sm.info, sm.tlsConfig, sm.hostname)

			log.Printf("Reconnect succesfull. Pool size is now %d\n", len(sm.sessions))

			break
		}
	}

	if len(sm.sessions) == 0 {
		log.Fatalf("All upstream sessions have been closed, no more upstream options available. Exiting.")
	}
}

// selectSession is a simple function that selects a session from the slice
func (sm *SessionManager) selectSession(index int) *yamux.Session {
	if len(sm.sessions) == 0 {
		// Handle the error appropriately here.
		// You could log an error, return a special value, etc.
		log.Fatalf("All upstream sessions have been closed, no more upstream options available. Exiting.")
		return nil // Returning nil as an example.
	}
	return sm.sessions[index%len(sm.sessions)]
}

// handleClientConnection is a function that handles the client connection
// func handleClientConnection(clientConn net.Conn, session *yamux.Session) {
func handleClientConnection(clientConn net.Conn, session *yamux.Session) {

	defer clientConn.Close()

	stream, err := session.OpenStream()
	if err != nil {
		log.Printf("Failed to open yamux stream: %v", err)
		return
	}
	defer stream.Close()

	go io.Copy(clientConn, stream)
	io.Copy(stream, clientConn)
}

func AddCommandsTo(client *cobra.Command) {
	client.AddCommand(clientProxyCmd)
	clientProxyCmd.Flags().IntVarP(&httpProxyPort, "port", "p", 8080, "port number to listen on")
	clientProxyCmd.Flags().IntVarP(&numStreams, "connections", "c", 1, "number of parallel connections to open to the Border0 service")
	clientProxyCmd.Flags().StringVarP(&hostname, "service", "", "", "The Border0 service identifier")
}
