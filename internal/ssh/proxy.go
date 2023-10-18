package ssh

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"time"

	"go.uber.org/zap"

	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/borderzero/border0-cli/internal/ssh/config"
	"github.com/borderzero/border0-cli/internal/ssh/session"
	"github.com/borderzero/border0-cli/internal/util"
	"github.com/borderzero/border0-go/types/common"
	"golang.org/x/crypto/ssh"
)

const (
	sshProxyVersion = "SSH-2.0-Border0.com"
)

type e2eSession struct {
	metadata    border0.E2EEncryptionMetadata
	proxyConfig config.ProxyConfig
}

func Proxy(l net.Listener, c config.ProxyConfig) error {
	if c.AwsUpstreamType != "" {

		// Use the aws profile from the top level config only
		// if the AwsCredentials object does not have an aws
		// profile defined. The aws profile on the aws creds
		// object comes from socket upstream configuration, so
		// it has higher priority than the aws profile defined
		// in the connector's configuration.
		if c.AWSProfile != "" {
			if c.AwsCredentials == nil {
				c.AwsCredentials = &common.AwsCredentials{}
			}
			if c.AwsCredentials.AwsProfile == nil {
				c.AwsCredentials.AwsProfile = &c.AWSProfile
			}
		}

		cfg, err := util.GetAwsConfig(context.Background(), c.AWSRegion, c.AwsCredentials)
		if err != nil {
			return fmt.Errorf("failed to initialize AWS client: %v", err)
		}
		c.AwsConfig = *cfg
	}

	if c.EndToEndEncryption {
		c.SshServerConfig = &ssh.ServerConfig{
			ServerVersion: sshProxyVersion,
		}
	} else {
		c.SshServerConfig = &ssh.ServerConfig{
			NoClientAuth: true,
		}
	}

	if c.Hostkey == nil {
		_, privateKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return fmt.Errorf("sshauthproxy: failed to generate private key: %s", err)
		}

		signer, err := ssh.NewSignerFromKey(privateKey)
		if err != nil {
			return fmt.Errorf("sshauthproxy: failed to generate signer: %s", err)
		}
		c.SshServerConfig.AddHostKey(signer)
	} else {
		c.SshServerConfig.AddHostKey(*c.Hostkey)
	}

	var handler session.SessionHandler

	switch {
	case c.Socket.SSHServer:
		handler = session.NewLocalSession(c.Logger, &c)
	case c.AwsUpstreamType == "aws-ssm":
		var err error
		handler, err = session.NewSsmSession(c.Logger, &c)
		if err != nil {
			return fmt.Errorf("failed to initialize AWS SSM session: %v", err)
		}
	case c.AwsUpstreamType == "aws-ec2connect":
		handler = session.NewEc2InstanceConnectSession(c.Logger, &c)
	default:
		var err error
		handler, err = session.NewSshSession(c.Logger, &c)
		if err != nil {
			return fmt.Errorf("failed to initialize SSH session: %v", err)
		}
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			c.Logger.Error("sshauthproxy: failed to accept connection", zap.Error(err))
			continue
		}

		go func() {
			if c.EndToEndEncryption {
				e2EEncryptionConn, ok := conn.(border0.E2EEncryptionConn)
				if !ok {
					conn.Close()
					c.Logger.Error("failed to cast connection to e2eencryption")
					return
				}

				e2eSession := &e2eSession{
					metadata:    *e2EEncryptionConn.Metadata,
					proxyConfig: c,
				}

				c.SshServerConfig.PublicKeyCallback = e2eSession.sshPublicKeyCallback
				c.SshServerConfig.AuthLogCallback = e2eSession.sshAuthLogCallback

				if c.Border0CertAuth {
					c.SshClientConfig.Auth = []ssh.AuthMethod{ssh.
						PublicKeysCallback(e2eSession.userPublicKeyCallback)}
				}
			}

			handler.Proxy(conn)
		}()
	}
}

func (s *e2eSession) userPublicKeyCallback() ([]ssh.Signer, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %s", err)
	}

	sshPublicKey, err := ssh.NewPublicKey(privateKey.Public())
	if err != nil {
		return nil, fmt.Errorf("failed to generate public key: %s", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	signedSshCert, err := s.proxyConfig.Border0API.SignSshOrgCertificate(ctx, s.proxyConfig.Socket.SocketID, s.metadata.SessionKey, s.metadata.UserEmail, s.metadata.SshTicket, ssh.MarshalAuthorizedKey(sshPublicKey))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch signed ssh org certificate %s", err)
	}

	pubcert, _, _, _, err := ssh.ParseAuthorizedKey([]byte(signedSshCert))
	if err != nil {
		return nil, fmt.Errorf("failed to parse authorized key: %s", err)
	}

	sshCert, ok := pubcert.(*ssh.Certificate)
	if !ok {
		return nil, fmt.Errorf("failed to cast to ssh certificate")
	}

	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to generate signer: %s", err)
	}

	certSigner, err := ssh.NewCertSigner(sshCert, signer)
	if err != nil {
		return nil, fmt.Errorf("failed to generate cert signer: %s", err)
	}

	return []ssh.Signer{certSigner}, nil
}

func (s *e2eSession) sshPublicKeyCallback(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	cert, ok := key.(*ssh.Certificate)
	if !ok {
		return nil, errors.New("can not cast certificate")
	}

	if s.proxyConfig.OrgSshCA == nil {
		return nil, errors.New("error: unable to validate certificate, no CA configured")
	}

	if bytes.Equal(cert.SignatureKey.Marshal(), s.proxyConfig.OrgSshCA.Marshal()) {
	} else {
		return nil, errors.New("error: invalid client certificate")
	}

	if s.metadata.UserEmail != cert.KeyId {
		return nil, errors.New("error: ssh certificate does not match tls certificate")
	}

	var certChecker ssh.CertChecker
	if err := certChecker.CheckCert("mysocket_ssh_signed", cert); err != nil {
		return nil, fmt.Errorf("error: invalid client certificate: %s", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	actions, _, err := s.proxyConfig.Border0API.Evaluate(ctx, s.proxyConfig.Socket, s.metadata.ClientIP, s.metadata.UserEmail, s.metadata.SessionKey)
	if err != nil {
		return nil, fmt.Errorf("error: failed to authorize: %s", err)
	}

	if len(actions) == 0 {
		return nil, errors.New("error: authorization failed")
	}

	return &ssh.Permissions{}, nil
}

func (s *e2eSession) sshAuthLogCallback(conn ssh.ConnMetadata, method string, err error) {
	if err != nil {
		if errors.Is(err, ssh.ErrNoAuth) {
			return
		}
		s.proxyConfig.Logger.Debug("sshauthproxy: authentication failed", zap.String("method", method), zap.String("user", conn.User()), zap.Error(err))
	} else {
		s.proxyConfig.Logger.Debug("sshauthproxy: authentication successful", zap.String("method", method), zap.String("user", conn.User()), zap.String("remote_addr", s.metadata.ClientIP), zap.String("userEmail", s.metadata.UserEmail))
	}
}
