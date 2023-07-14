package border0

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/borderzero/border0-cli/internal/api"
	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/cenkalti/backoff/v4"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"golang.org/x/net/proxy"
)

const (
	defaultTunnelHost                     = "tunnel.border0.com"
	defaultTunnelPort                     = 22
	defaultSSHTimeout                     = 5 * time.Second
	tunnelHostEnvVar                      = "BORDER0_TUNNEL"
	connectorAuthenticationHeader         = "BORDER0-CLIENT-CONNECTOR-AUTHENTICATED"
	connectorAuthenticationShutdownTime   = 200 * time.Millisecond
	connectorAuthenticationCertificateOrg = "Border0 Connector"
	connectorAuthenticationCertificateTTL = 24 * 365 * 10 * time.Hour
	connectorAuthenticationTimeout        = 10 * time.Second
	proxyHostRegex                        = "^http(s)?://"
)

type Border0API interface {
	GetUserID() (string, error)
	SignSSHKey(ctx context.Context, socketID string, publicKey []byte) (string, string, error)
}

type Socket struct {
	SocketID                         string
	SocketType                       string
	UpstreamType                     string
	ConnectorAuthenticationEnabled   bool
	ConnectorAuthenticationTLSConfig *tls.Config
	border0API                       Border0API
	keyPair                          sshKeyPair
	sshSigner                        ssh.Signer
	tunnelHost                       string
	errChan                          chan error
	readyChan                        chan bool
	listener                         net.Listener
	Organization                     *models.Organization
	proxyHost                        *url.URL
	version                          string
	hostKey                          ssh.PublicKey
	acceptChan                       chan connWithError
	context                          context.Context
	cancel                           context.CancelFunc
	Socket                           *models.Socket
}

type connWithError struct {
	conn net.Conn
	err  error
}

type sshKeyPair struct {
	privateKey []byte
	publicKey  []byte
}

type border0NetError string

func (e border0NetError) Error() string   { return "Border0 network error " + string(e) }
func (e border0NetError) Timeout() bool   { return false }
func (e border0NetError) Temporary() bool { return true }

type PermanentError struct {
	Message string
}

func (e PermanentError) Error() string { return e.Message }

func NewSocket(ctx context.Context, border0API api.API, nameOrID string) (*Socket, error) {
	socketFromApi, err := border0API.GetSocket(ctx, nameOrID)
	if err != nil {
		return nil, err
	}

	org, err := border0API.GetOrganizationInfo(ctx)
	if err != nil {
		return nil, err
	}

	sckContext, sckCancel := context.WithCancel(context.Background())

	return &Socket{
		SocketID:                       socketFromApi.SocketID,
		SocketType:                     socketFromApi.SocketType,
		UpstreamType:                   socketFromApi.UpstreamType,
		ConnectorAuthenticationEnabled: socketFromApi.ConnectorAuthenticationEnabled,
		border0API:                     border0API,
		tunnelHost:                     TunnelHost(),
		errChan:                        make(chan error),
		readyChan:                      make(chan bool),
		Organization:                   org,
		acceptChan:                     make(chan connWithError),

		context: sckContext,
		cancel:  sckCancel,
	}, nil
}

func NewSocketFromConnectorAPI(ctx context.Context, border0API Border0API, socket models.Socket, org *models.Organization) (*Socket, error) {
	newCtx, cancel := context.WithCancel(context.Background())

	return &Socket{
		SocketID:                       socket.SocketID,
		SocketType:                     socket.SocketType,
		UpstreamType:                   socket.UpstreamType,
		ConnectorAuthenticationEnabled: socket.ConnectorAuthenticationEnabled,
		border0API:                     border0API,
		tunnelHost:                     TunnelHost(),
		errChan:                        make(chan error),
		readyChan:                      make(chan bool),
		Organization:                   org,
		acceptChan:                     make(chan connWithError),
		Socket:                         &socket,

		context: newCtx,
		cancel:  cancel,
	}, nil
}

func (s *Socket) WithVersion(version string) {
	s.version = version
}

func (s *Socket) WithProxy(proxyHost string) error {
	proxyMatch, err := regexp.Compile(proxyHostRegex)
	if err != nil {
		return fmt.Errorf("failed to compile proxy regex: %s", err)
	}

	if !proxyMatch.MatchString(proxyHost) {
		return fmt.Errorf("invalid proxy URL: %s", proxyHost)
	}

	proxyURL, err := url.Parse(proxyHost)
	if err != nil {
		return fmt.Errorf("invalid proxy URL: %s", err)
	}

	s.proxyHost = proxyURL
	return nil
}

func (s *Socket) Listen() (net.Listener, error) {
	if s.ConnectorAuthenticationEnabled {
		tlsConfig, err := s.generateConnectorAuthenticationTLSConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to generate connector authentication TLS config: %v", err)
		}

		s.ConnectorAuthenticationTLSConfig = tlsConfig
	}

	go s.tunnelConnect()

	go func() {
		for {
			select {
			case err := <-s.errChan:
				log.Printf("border0 listener: %v", err)
				if _, ok := err.(PermanentError); ok {
					log.Printf("border0 listener: permanent error, exiting")
					s.cancel()
					return
				}
			case <-s.context.Done():
				return
			}
		}
	}()

	select {
	case <-s.context.Done():
		return nil, s.context.Err()
	case <-s.readyChan:
	}

	newConn := make(chan net.Conn)
	go func() {
		for {
			conn, err := s.listener.Accept()
			if err != nil {
				select {
				case <-s.context.Done():
					return
				default:
				}
				if err == io.EOF {
					log.Print("listener closed, reconnecting...")
					<-s.readyChan
					continue
				} else {
					log.Printf("error accepting connecting %s", err)
					continue
				}
			}

			newConn <- conn
		}
	}()

	go func() {
		for {
			select {
			case conn := <-newConn:
				if s.ConnectorAuthenticationEnabled {
					go s.acceptConnectorAuth(conn)
				} else {
					s.acceptChan <- connWithError{conn, nil}
				}
			case <-s.context.Done():
				s.listener.Close()
				return
			}
		}
	}()

	return s, nil
}

func (s *Socket) tunnelConnect() {
	defer close(s.errChan)
	if err := s.generateSSHKeyPair(); err != nil {
		s.errChan <- PermanentError{fmt.Sprintf("failed to generate ssh key pair: %v", err)}
		return
	}

	userID, err := s.border0API.GetUserID()
	if err != nil {
		s.errChan <- PermanentError{fmt.Sprintf("failed to get userid from token: %v", err)}
		return
	}

	sshConfig := &ssh.ClientConfig{
		User:          userID,
		Timeout:       defaultSSHTimeout,
		ClientVersion: "SSH-2.0-Border0-" + s.version,
	}

	ebackoff := backoff.NewExponentialBackOff()
	ebackoff.MaxElapsedTime = 0
	ebackoff.MaxInterval = 5 * time.Minute

	err = backoff.Retry(func() error {
		if err := s.refreshSSHCert(); err != nil {
			s.errChan <- fmt.Errorf("failed to refresh tunnel certificate: %s", err)
			return err
		}

		sshConfig.HostKeyCallback = ssh.FixedHostKey(s.hostKey)
		sshConfig.Auth = []ssh.AuthMethod{ssh.PublicKeys(s.sshSigner)}

		if err = s.sshConnect(sshConfig, ebackoff); err != nil {
			s.errChan <- fmt.Errorf("failed to connect to server: %s", err)
			return err
		}

		return nil
	}, ebackoff)

	if err != nil {
		s.errChan <- fmt.Errorf("failed to connect to server: %s", err)
		return
	}
}

func (s *Socket) sshConnect(config *ssh.ClientConfig, b backoff.BackOff) error {
	var dialer proxy.Dialer
	var err error

	if s.proxyHost != nil {
		proxy.RegisterDialerType("http", newHttpProxy)
		proxy.RegisterDialerType("https", newHttpProxy)

		dialer, err = proxy.FromURL(s.proxyHost, proxy.Direct)
		if err != nil {
			return fmt.Errorf("failed to create proxy dialer: %v", err)
		}
	} else {
		dialer = proxy.Direct
	}

	conn, err := dialer.Dial("tcp", s.tunnelHost)
	if err != nil {
		return fmt.Errorf("failed to connect to tunnel service: %s", err)
	}
	defer conn.Close()

	sshCon, channel, req, err := ssh.NewClientConn(conn, s.tunnelHost, config)
	if err != nil {
		return fmt.Errorf("failed to setup connection to tunnel service: %s", err)
	}
	defer sshCon.Close()

	client := ssh.NewClient(sshCon, channel, req)
	defer client.Close()

	ctx, cancel := context.WithCancel(s.context)
	defer cancel()

	go s.keepAlive(ctx, client)

	s.listener, err = client.Listen("tcp", "localhost:0")
	if err != nil {
		return fmt.Errorf("failed to open listener on tunnel server: %w", err)
	}

	defer s.listener.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %v", err)
	}
	defer session.Close()

	session.Stdout = os.Stdout
	modes := ssh.TerminalModes{}

	if err := session.RequestPty("xterm-256color", 80, 40, modes); err != nil {
		return fmt.Errorf("request for pseudo terminal failed %v", err)
	}

	if err := session.Shell(); err != nil {
		return err
	}

	s.readyChan <- true

	sessionStoppedChan := make(chan error, 1)

	go func() {
		defer close(sessionStoppedChan)

		err := session.Wait()
		cancel()
		sessionStoppedChan <- err
	}()

	select {
	case <-s.context.Done():
		return nil
	case err := <-sessionStoppedChan:
		if err != nil {
			b.Reset()
		}

		return err
	}
}

func (s *Socket) generateSSHKeyPair() error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	var privBuf bytes.Buffer
	parsed, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}

	privPEM := &pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: parsed,
	}

	if err := pem.Encode(&privBuf, privPEM); err != nil {
		return err
	}

	pub, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		return err
	}

	s.keyPair = sshKeyPair{
		privateKey: bytes.TrimSpace(privBuf.Bytes()),
		publicKey:  bytes.TrimSpace(ssh.MarshalAuthorizedKey(pub)),
	}

	return nil
}

func (s *Socket) refreshSSHCert() error {
	sshCert, hostKey, err := s.border0API.SignSSHKey(context.Background(), s.SocketID, s.keyPair.publicKey)
	if err != nil {
		return err
	}

	certData := []byte(sshCert)
	pubcert, _, _, _, err := ssh.ParseAuthorizedKey(certData)
	if err != nil {
		return fmt.Errorf("failed to parse ssh certificate: %v", err)
	}

	cert, ok := pubcert.(*ssh.Certificate)
	if !ok {
		return fmt.Errorf("error failed to cast to certificate: %v", err)
	}

	clientKey, err := ssh.ParsePrivateKey(s.keyPair.privateKey)
	if err != nil {
		return fmt.Errorf("failed to parse private key: %v", err)
	}

	if s.sshSigner, err = ssh.NewCertSigner(cert, clientKey); err != nil {
		return fmt.Errorf("failed to create signer: %v", err)
	}

	hostKeyBytes, err := base64.StdEncoding.DecodeString(hostKey)
	if err != nil {
		return fmt.Errorf("failed to decode hostkey %v", err)
	}

	if s.hostKey, err = ssh.ParsePublicKey(hostKeyBytes); err != nil {
		return fmt.Errorf("failed to parse hostkey %v", err)
	}

	return nil
}

func TunnelHost() string {
	if os.Getenv(tunnelHostEnvVar) != "" {
		if !strings.Contains(os.Getenv(tunnelHostEnvVar), ":") {
			return net.JoinHostPort(os.Getenv(tunnelHostEnvVar), strconv.Itoa(defaultTunnelPort))
		}
		return os.Getenv(tunnelHostEnvVar)
	} else {
		return net.JoinHostPort(defaultTunnelHost, strconv.Itoa(defaultTunnelPort))
	}
}

func (s *Socket) keepAlive(ctx context.Context, client *ssh.Client) {
	t := time.NewTicker(10 * time.Second)
	max := 4
	n := 0

	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			aliveChan := make(chan bool, 1)

			go func() {
				_, _, err := client.SendRequest("keepalive@border0.com", true, nil)
				if err != nil {
					aliveChan <- false
				} else {
					aliveChan <- true
				}
			}()

			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				n++
			case alive := <-aliveChan:
				if !alive {
					n++
				} else {
					n = 0
				}
			}

			if n >= max {
				log.Println("ssh keepalive timeout, disconnecting")
				client.Close()
				return
			}
		}
	}
}

func (s *Socket) Accept() (net.Conn, error) {
	if s.listener == nil {
		return nil, fmt.Errorf("no listener")
	}

	r := <-s.acceptChan
	return r.conn, r.err
}

func (s *Socket) acceptConnectorAuth(conn net.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), connectorAuthenticationTimeout)
	defer cancel()

	ok, err := s.authenticateConnector(ctx, conn)
	if err != nil {
		conn.Close()
		log.Printf("failed to authenticate connector %s", err)
		s.acceptChan <- connWithError{nil, border0NetError(fmt.Sprintf("failed to authenticate connector: %s", err))}
		return
	}

	if !ok {
		conn.Close()
		log.Print("failed to authenticate connector")
		s.acceptChan <- connWithError{nil, border0NetError("failed to authenticate connector")}
		return
	}

	s.acceptChan <- connWithError{conn, nil}
}

func (s *Socket) Addr() net.Addr {
	return s.listener.Addr()
}

func (s *Socket) Close() error {
	s.cancel()

	if s.listener != nil {
		return s.listener.Close()
	}

	return nil
}

func (s *Socket) authenticateConnector(ctx context.Context, conn net.Conn) (bool, error) {
	tlsConn := tls.Server(conn, s.ConnectorAuthenticationTLSConfig)

	authChan := make(chan error, 1)
	go func() {
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			authChan <- fmt.Errorf("client tls handshake failed: %s", err)
			return
		}

		if _, err := conn.Write([]byte(connectorAuthenticationHeader)); err != nil {
			authChan <- fmt.Errorf("failed to write header: %s", err)
			return
		}

		authChan <- nil
	}()

	select {
	case err := <-authChan:
		if err != nil {
			return false, err
		}
	case <-ctx.Done():
		return false, fmt.Errorf("client handshake failed: %s", ctx.Err())
	}

	log.Printf("client %s authenticated", tlsConn.ConnectionState().PeerCertificates[0].Subject.CommonName)
	time.Sleep(connectorAuthenticationShutdownTime)

	return true, nil
}

func (s *Socket) generateConnectorAuthenticationTLSConfig() (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate ecdsa key: %s", err)
	}

	keyDer, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ecdsa key: %s", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   s.SocketID,
			Organization: []string{connectorAuthenticationCertificateOrg},
		},
		IsCA:                  true,
		NotBefore:             time.Now().Add(-1 * time.Minute),
		NotAfter:              time.Now().Add(connectorAuthenticationCertificateTTL),
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		DNSNames:              []string{s.SocketID},
	}

	certDer, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate: %s", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDer})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer})

	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate: %s", err)
	}

	caCertPool := x509.NewCertPool()
	if caCert, ok := s.Organization.Certificates["mtls_certificate"]; !ok {
		return nil, fmt.Errorf("no organization ca certificate found")
	} else {
		if ok := caCertPool.AppendCertsFromPEM([]byte(caCert)); !ok {
			return nil, fmt.Errorf("failed to parse ca certificate")
		}
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caCertPool,
	}, nil
}

func ProxyConnection(client net.Conn, remote net.Conn) {
	defer client.Close()
	defer remote.Close()

	chDone := make(chan bool, 1)

	go func() {
		io.Copy(client, remote)
		chDone <- true
	}()

	go func() {
		io.Copy(remote, client)
		chDone <- true
	}()

	<-chDone
}

func Serve(logger *zap.Logger, l net.Listener, hostname string, port int) error {
	for {
		rconn, err := l.Accept()
		if err != nil {
			return fmt.Errorf("failed to accept connection: %s", err)
		}

		go func() {
			lconn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", hostname, port), 5*time.Second)
			if err != nil {
				logger.Sugar().Errorf("failed to connect to local service: %s", err)
				rconn.Close()
				return
			}

			go ProxyConnection(rconn, lconn)
		}()
	}
}

func (s *Socket) IsClosed() bool {
	select {
	case <-s.context.Done():
		return true
	default:
		return false
	}
}
