package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"

	"go.uber.org/zap"

	"github.com/borderzero/border0-cli/internal/ssh/config"
	"github.com/borderzero/border0-cli/internal/ssh/session"
	"github.com/borderzero/border0-cli/internal/util"
	"github.com/borderzero/border0-go/types/common"
	"golang.org/x/crypto/ssh"
)

const (
	sshProxyVersion = "SSH-2.0-Border0.com"
)

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
		handler = session.NewLocalSessionHandler(c.Logger, &c)
	case c.AwsUpstreamType == "aws-ssm":
		var err error
		handler, err = session.NewSsmSessionHandler(c.Logger, &c)
		if err != nil {
			return fmt.Errorf("failed to initialize AWS SSM session: %v", err)
		}
	case c.AwsUpstreamType == "aws-ec2connect":
		handler = session.NewEc2InstanceConnectSessionHandler(c.Logger, &c)
	default:
		var err error
		handler, err = session.NewSshSessionHandler(c.Logger, &c)
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

		go handler.Proxy(conn)
	}
}
