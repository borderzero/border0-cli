package sqlauthproxy

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"log"
	"math/big"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/jackc/pgconn"
	"github.com/jackc/pgproto3/v2"
)

type postgresHandler struct {
	Config
	UpstreamConfig *pgconn.Config
	awsCredentials aws.CredentialsProvider
	tlsConfig      *tls.Config
}

func newPostgresHandler(c Config) (*postgresHandler, error) {
	var awsCredentials aws.CredentialsProvider
	if c.RdsIam {
		cfg, err := config.LoadDefaultConfig(context.TODO())
		if err != nil {
			return nil, fmt.Errorf("failed to load aws config: %s", err)
		}

		awsCredentials = cfg.Credentials
	}

	cert, err := generateX509KeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate tls certificate: %s", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	var sslSettings []string
	if c.UpstreamTLS && c.DialerFunc == nil {
		if c.UpstreamCAFile != "" {
			sslSettings = append(sslSettings, fmt.Sprintf("sslrootcert=%s", c.UpstreamCAFile))
			sslSettings = append(sslSettings, "sslmode=verify-ca")
		} else {
			sslSettings = append(sslSettings, "sslmode=require")
		}

		if c.UpstreamCertFile != "" {
			sslSettings = append(sslSettings, fmt.Sprintf("sslcert=%s", c.UpstreamCertFile))
		}

		if c.UpstreamKeyFile != "" {
			sslSettings = append(sslSettings, fmt.Sprintf("sslkey=%s", c.UpstreamKeyFile))
		}
	} else {
		sslSettings = append(sslSettings, "sslmode=prefer")
	}

	var strSslSettings string
	if len(sslSettings) > 0 {
		strSslSettings = "?" + strings.Join(sslSettings, "&")
	}

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d%s", c.Username, c.Password, c.Hostname, c.Port, strSslSettings)
	config, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}

	if c.DialerFunc != nil {
		config.DialFunc = c.DialerFunc
	}

	return &postgresHandler{
		Config:         c,
		UpstreamConfig: config,
		awsCredentials: awsCredentials,
		tlsConfig:      tlsConfig,
	}, nil
}

func (h postgresHandler) handleClient(c net.Conn) {
	defer c.Close()

	startupMessage, c, err := h.handleClientStartup(c)
	if err != nil {
		log.Printf("sqlauthproxy: failed to handle client startup: %s", err)
		return
	}

	clientConn := pgproto3.NewBackend(pgproto3.NewChunkReader(c), c)

	if startupMessage == nil {
		log.Printf("sqlauthproxy: failed to handle client startup")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if db, ok := startupMessage.Parameters["database"]; ok {
		h.UpstreamConfig.Database = db
	}

	if h.RdsIam {
		authenticationToken, err := auth.BuildAuthToken(context.TODO(), net.JoinHostPort(h.Hostname, strconv.Itoa(h.Port)), h.AwsRegion, h.Username, h.awsCredentials)
		if err != nil {
			log.Printf("sqlauthproxy: failed to create authentication token: %s", err)
			return
		}

		h.UpstreamConfig.Password = authenticationToken
	}

	conn, err := pgconn.ConnectConfig(ctx, h.UpstreamConfig)
	if err != nil {
		log.Printf("sqlauthproxy: failed to connect to upstream: %s", err)
		return
	}

	pgconn, err := conn.Hijack()
	if err != nil {
		log.Printf("sqlauthproxy: failed to connect to upstream: %s", err)
		return
	}

	if err = h.handleClientAuthRequest(clientConn, pgconn.ParameterStatuses); err != nil {
		log.Printf("sqlauthproxy: failed to handle client authentication: %s", err)
		return
	}

	border0.ProxyConnection(c, pgconn.Conn)
}

func (h postgresHandler) handleClientStartup(conn net.Conn) (*pgproto3.StartupMessage, *tls.Conn, error) {
	c := pgproto3.NewBackend(pgproto3.NewChunkReader(conn), conn)

	message, err := c.ReceiveStartupMessage()
	if err != nil {
		return nil, nil, err
	}

	switch msg := message.(type) {
	case *pgproto3.StartupMessage:
		tlsConn, ok := conn.(*tls.Conn)
		if !ok {
			return nil, nil, fmt.Errorf("failed to get TLS connection info")
		}

		return msg, tlsConn, nil
	case *pgproto3.SSLRequest:
		_, err = conn.Write([]byte("S"))
		if err != nil {
			return nil, nil, err
		}

		conn = tls.Server(conn, h.tlsConfig)
		return h.handleClientStartup(conn)
	case *pgproto3.CancelRequest:
		conn.Close()
		return nil, nil, nil
	default:
		return nil, nil, fmt.Errorf("invalid startup message (%T)", msg)
	}
}

func (h postgresHandler) handleClientAuthRequest(serverSession *pgproto3.Backend, serverParams map[string]string) error {
	err := serverSession.Send(&pgproto3.AuthenticationOk{})
	if err != nil {
		return err
	}

	for name, value := range serverParams {
		err = serverSession.Send(&pgproto3.ParameterStatus{
			Name:  name,
			Value: value,
		})
		if err != nil {
			return err
		}
	}

	err = serverSession.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
	if err != nil {
		return err
	}

	return nil
}

func generateX509KeyPair() (tls.Certificate, error) {
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.Unix()),
		Subject: pkix.Name{
			CommonName:   "sqlauthproxy",
			Organization: []string{"border.com"},
		},
		NotBefore: now,
		NotAfter:  now.AddDate(10, 0, 0),
	}

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}

	cert, err := x509.CreateCertificate(rand.Reader, template, template,
		priv.Public(), priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	var outCert tls.Certificate
	outCert.Certificate = append(outCert.Certificate, cert)
	outCert.PrivateKey = priv

	return outCert, nil
}
