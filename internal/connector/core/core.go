package core

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/borderzero/border0-cli/internal/api"
	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/borderzero/border0-cli/internal/connector/config"
	"github.com/borderzero/border0-cli/internal/connector/discover"
	"github.com/borderzero/border0-cli/internal/sqlauthproxy"
	"github.com/borderzero/border0-cli/internal/ssh"
	sshConfig "github.com/borderzero/border0-cli/internal/ssh/config"
	"github.com/borderzero/border0-cli/internal/ssh/server"
	"github.com/borderzero/border0-cli/internal/util"
	"github.com/borderzero/border0-go/lib/types/pointer"
	"go.uber.org/zap"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"
)

type connectTunnelData struct {
	key    string
	socket models.Socket
	action string
}
type ConnectorCore struct {
	numberOfRuns int64
	discovery    discover.Discover
	cfg          config.Config
	border0API   api.API
	logger       *zap.Logger
	version      string

	discoverState            discover.DiscoverState
	connectChan              chan connectTunnelData
	connectedTunnels         sync.Map
	sshPrivateHostKeyLock    sync.Mutex
	sshPrivateHostKey        *gossh.Signer
	connectorCertificateLock sync.Mutex
	connectorCertificate     *tls.Certificate

	metadata models.Metadata // additionall metadata
}

func NewConnectorCore(logger *zap.Logger, cfg config.Config, discovery discover.Discover, border0API api.API, meta models.Metadata, version string) *ConnectorCore {
	connectChan := make(chan connectTunnelData, 5)
	discoverState := discover.DiscoverState{
		State:     make(map[string]interface{}),
		RunsCount: 0,
	}

	return &ConnectorCore{
		connectChan: connectChan,
		logger:      logger, discovery: discovery, cfg: cfg,
		border0API:    border0API,
		discoverState: discoverState,
		metadata:      meta,
		version:       version,
	}
}

func (c *ConnectorCore) IsSocketConnected(key string) bool {
	session, ok := c.connectedTunnels.Load(key)
	if ok {
		if session.(*ssh.Connection).IsClosed() {
			return false
		}
	}

	return ok
}

func (c *ConnectorCore) TunnelConnnect(ctx context.Context, socket models.Socket) error {
	conn, err := ssh.NewConnection(c.logger, c.border0API, socket.SocketID, c.version)
	if err != nil {
		return err
	}

	c.connectedTunnels.Store(socket.SocketID, conn)
	defer c.connectedTunnels.Delete(socket.SocketID)

	// reload socket
	socketFromApi, err := c.border0API.GetSocket(ctx, socket.SocketID)
	if err != nil {
		return err
	}
	socketFromApi.ConnectorLocalData = socket.ConnectorLocalData

	socket = *socketFromApi
	socket.BuildConnectorDataByTags()

	var handlerConfig *sqlauthproxy.Config
	if socket.SocketType == "database" {
		handlerConfig, err = sqlauthproxy.BuildHandlerConfig(c.logger, socket, c.border0API)
		if err != nil {
			return fmt.Errorf("failed to create config for socket: %s", err)
		}
	}

	var sshProxyConfig *sshConfig.ProxyConfig
	if socket.SocketType == "ssh" {
		var hostkeySigner *gossh.Signer
		var org *models.Organization
		if socket.EndToEndEncryptionEnabled {
			hostkeySigner, err = c.hostkey()
			if err != nil {
				return fmt.Errorf("failed to get hostkey: %s", err)
			}

			org, err = c.border0API.GetOrganizationInfo(ctx)
			if err != nil {
				return fmt.Errorf("failed to get organization info: %s", err)
			}
		}

		sshProxyConfig, err = sshConfig.BuildProxyConfig(c.logger, socket, c.cfg.Connector.AwsRegion, c.cfg.Connector.AwsProfile, hostkeySigner, org, c.border0API)
		if err != nil {
			return fmt.Errorf("failed to create config for socket: %s", err)
		}
	}

	var certificate *tls.Certificate
	if socket.EndToEndEncryptionEnabled {
		certificate, err = c.certificate(ctx, conn.Socket.Organization.ID)
		if err != nil {
			return fmt.Errorf("failed to get connector certificate: %w", err)
		}

		conn.Socket.WithCertificate(certificate)
	}

	time.Sleep(1 * time.Second)
	l, err := conn.Socket.Listen()
	if err != nil {
		return err
	}

	switch {
	case socket.ConnectorLocalData != nil && socket.ConnectorLocalData.SSHServer && socket.SocketType == "ssh":
		sshServer, err := server.NewServer(c.logger, conn.Socket.Organization.Certificates["ssh_public_key"])
		if err != nil {
			return err
		}

		if err := sshServer.Serve(l); err != nil {
			return err
		}
	case sshProxyConfig != nil:
		if err := ssh.Proxy(l, *sshProxyConfig); err != nil {
			return err
		}
	case handlerConfig != nil:
		if err := sqlauthproxy.Serve(l, *handlerConfig); err != nil {
			return err
		}
	default:
		if err := border0.Serve(c.logger, l, socket.ConnectorData.TargetHostname, socket.ConnectorData.Port); err != nil {
			return err
		}
	}

	return nil
}

func (c *ConnectorCore) HandleUpdates(ctx context.Context, sockets []models.Socket) error {
	sockets, err := c.SocketsCoreHandler(ctx, sockets)
	if err != nil {
		log.Printf("failed to check new sockets: %v", err)
		return err
	}

	for _, socket := range sockets {
		if !c.IsSocketConnected(socket.SocketID) {
			c.logger.Info("found new socket to connect")

			c.connectChan <- connectTunnelData{
				key:    socket.SocketID,
				socket: socket,
				action: "connect"}
		}
	}

	return nil
}

func (c *ConnectorCore) TunnelConnectJob(ctx context.Context, group *errgroup.Group) {
	group.Go(func() error {
		for {
			select {
			case <-ctx.Done():
				return errors.New("context canceled")
			case tunnelConnectData := <-c.connectChan:
				if tunnelConnectData.action == "connect" {
					group.Go(func() error {
						err := c.TunnelConnnect(ctx, tunnelConnectData.socket)
						if err != nil {
							c.logger.Error("error connecting to tunnel", zap.String("error", err.Error()))
						}

						return nil
					})
				}

				if tunnelConnectData.action == "disconnect" {
					if session, ok := c.connectedTunnels.Load(tunnelConnectData.key); ok {
						session.(*ssh.Connection).Close()
					}
				}
			}
		}
	})
}

func (c *ConnectorCore) DiscoverNewSocketChanges(ctx context.Context, ch chan []models.Socket) {
	c.discoverState.RunsCount = c.numberOfRuns

	if c.discovery.SkipRun(ctx, c.cfg, c.discoverState) {
		return
	}
	if c.numberOfRuns != 0 {
		seconds := c.discovery.WaitSeconds()
		time.Sleep(time.Duration(seconds) * time.Second)
	}

	sockets, err := c.discovery.Find(ctx, c.cfg, c.discoverState)
	if err != nil {
		c.logger.Error("error discovering new sockets", zap.Error(err))
		return
	}

	for i, s := range sockets {
		s.BuildConnectorDataAndTags(c.cfg.Connector.Name, c.metadata)
		sockets[i] = s
	}

	atomic.AddInt64(&c.numberOfRuns, 1)
	ch <- sockets
}

func (c *ConnectorCore) SocketsCoreHandler(ctx context.Context, socketsToUpdate []models.Socket) ([]models.Socket, error) {
	logger := c.logger.With(zap.String("plugin_name", c.discovery.Name()))
	var socketsToConnect []models.Socket

	discoveredSockets := socketsToUpdate

	// boostrap sockets coming from the discovery
	localSocketsMap := make(map[string]models.Socket)
	for i, socket := range discoveredSockets {
		socket.PluginName = c.discovery.Name()
		socket.SanitizeName()
		socket.BuildConnectorData(c.cfg.Connector.Name, c.metadata)
		socket.Tags = socket.ConnectorData.Tags()
		socket.SetupTypeAndUpstreamTypeByPortOrTags()
		localSocketsMap[socket.ConnectorData.Key()] = socket

		// update socket in the list
		discoveredSockets[i] = socket
	}

	socketsFromApi, err := c.border0API.GetSockets(ctx)
	if err != nil {
		return nil, err
	}

	socketApiMap := make(map[string]models.Socket)
	for _, socket := range socketsFromApi {
		socket.BuildConnectorDataByTags()
		// filter api sockets by connector name
		if socket.ConnectorData != nil && socket.ConnectorData.Key() != "" {
			for _, policy := range socket.Policies {
				socket.PolicyNames = append(socket.PolicyNames, policy.Name)
			}

			socketApiMap[socket.ConnectorData.Key()] = socket
		}
	}

	connectedTunnelsSize := 0
	c.connectedTunnels.Range(func(_, _ interface{}) bool {
		connectedTunnelsSize++
		return true
	})

	logger.Info("sockets found",
		zap.Int("local connector sockets", len(discoveredSockets)),
		zap.Int("api sockets", len(socketsFromApi)),
		zap.Int("connected sockets", connectedTunnelsSize))

	if err := c.CheckSocketsToDelete(ctx, socketApiMap, localSocketsMap); err != nil {
		return nil, err
	}

	socketsToConnect, errC := c.CheckSocketsToCreate(ctx, discoveredSockets, socketApiMap)
	if errC != nil {
		logger.Error("error checking sockets to create", zap.Error(errC))
		return nil, errC
	}

	if err := c.checkTunnelConnections(ctx, socketApiMap); err != nil {
		logger.Error("error checking tunnel connections", zap.Error(err))
		return nil, err
	}

	logger.Info("number of sockets to connect: ", zap.Int("sockets to connect", len(socketsToConnect)))
	return socketsToConnect, nil
}

func (c *ConnectorCore) checkTunnelConnections(ctx context.Context, socketApiMap map[string]models.Socket) error {

	c.connectedTunnels.Range(func(socketID, _ interface{}) bool {
		var found bool
		for _, socket := range socketApiMap {
			if socket.SocketID == socketID {
				found = true
				break
			}
		}

		if !found {
			c.logger.Info("socket not local, disconnecting", zap.String("socket_id", socketID.(string)))
			c.connectChan <- connectTunnelData{
				key:    socketID.(string),
				action: "disconnect"}
		}

		return true
	})

	return nil
}

func (c *ConnectorCore) shouldUpdateSocket(apiSocket, localSocket models.Socket) bool {
	// check allowed email addresses
	if !stringSlicesEqual(apiSocket.AllowedEmailAddresses, localSocket.AllowedEmailAddresses) {
		c.logger.Debug(
			"socket needs update, AllowedEmailAddresses mismatch",
			zap.Any("api_allowed_email_addresses", apiSocket.AllowedEmailAddresses),
			zap.Any("local_allowed_email_addresses", localSocket.AllowedEmailAddresses),
		)
		return true
	}
	if !stringSlicesEqual(localSocket.AllowedEmailAddresses, apiSocket.AllowedEmailAddresses) {
		c.logger.Debug(
			"socket needs update, AllowedEmailAddresses mismatch",
			zap.Any("api_allowed_email_addresses", apiSocket.AllowedEmailAddresses),
			zap.Any("local_allowed_email_addresses", localSocket.AllowedEmailAddresses),
		)
		return true
	}
	// check allowed email domains
	if !stringSlicesEqual(apiSocket.AllowedEmailDomains, localSocket.AllowedEmailDomains) {
		c.logger.Debug(
			"socket needs update, AllowedEmailDomains mismatch",
			zap.Any("api_allowed_email_domains", apiSocket.AllowedEmailDomains),
			zap.Any("local_allowed_email_addresses", localSocket.AllowedEmailDomains),
		)
		return true
	}
	if !stringSlicesEqual(localSocket.AllowedEmailDomains, apiSocket.AllowedEmailDomains) {
		c.logger.Debug(
			"socket needs update, AllowedEmailDomains mismatch",
			zap.Any("api_allowed_email_domains", apiSocket.AllowedEmailDomains),
			zap.Any("local_allowed_email_addresses", localSocket.AllowedEmailDomains),
		)
		return true
	}

	// check policy names
	if !stringSlicesEqual(apiSocket.PolicyNames, localSocket.PolicyNames) {
		c.logger.Debug(
			"socket needs update, PolicyNames mismatch",
			zap.Any("api_policy_names", apiSocket.PolicyNames),
			zap.Any("local_policy_names", localSocket.PolicyNames),
		)
		return true
	}
	if !stringSlicesEqual(localSocket.PolicyNames, apiSocket.PolicyNames) {
		c.logger.Debug(
			"socket needs update, PolicyNames mismatch",
			zap.Any("api_policy_names", apiSocket.PolicyNames),
			zap.Any("local_policy_names", localSocket.PolicyNames),
		)
		return true
	}

	// check http hostname
	if pointer.ValueOrZero(apiSocket.UpstreamHttpHostname) != pointer.ValueOrZero(localSocket.UpstreamHttpHostname) {
		c.logger.Debug(
			"socket needs update, UpstreamHttpHostname mismatch",
			zap.Any("api_upstream_http_hostname", apiSocket.UpstreamHttpHostname),
			zap.Any("local_upstream_http_hostname", localSocket.UpstreamHttpHostname),
		)
		return true
	}

	// check upstream type
	if apiSocket.UpstreamType != localSocket.UpstreamType {
		c.logger.Debug(
			"socket needs update, UpstreamType mismatch",
			zap.Any("api_upstream_type", apiSocket.UpstreamType),
			zap.Any("local_upstream_type", localSocket.UpstreamType),
		)
		return true
	}

	// check upstream username
	if pointer.ValueOrZero(apiSocket.UpstreamUsername) != pointer.ValueOrZero(localSocket.UpstreamUsername) {
		c.logger.Debug(
			"socket needs update, UpstreamUsername mismatch",
			zap.Any("api_upstream_username", pointer.ValueOrZero(apiSocket.UpstreamUsername)),
			zap.Any("local_upstream_username", pointer.ValueOrZero(localSocket.UpstreamUsername)),
		)
		return true
	}

	// check upstream password
	if pointer.ValueOrZero(apiSocket.UpstreamPassword) != pointer.ValueOrZero(localSocket.UpstreamPassword) {
		c.logger.Debug(
			"socket needs update, UpstreamPassword mismatch",
			zap.Any("api_upstream_password", pointer.ValueOrZero(apiSocket.UpstreamPassword)),
			zap.Any("local_upstream_password", pointer.ValueOrZero(localSocket.UpstreamPassword)),
		)
		return true
	}

	// check connector authentication enabled
	if apiSocket.ConnectorAuthenticationEnabled != localSocket.ConnectorAuthenticationEnabled {
		c.logger.Debug(
			"socket needs update, ConnectorAuthenticationEnabled mismatch",
			zap.Any("api_connector_authentication_enabled", apiSocket.ConnectorAuthenticationEnabled),
			zap.Any("local_connector_authentication_enabled", localSocket.ConnectorAuthenticationEnabled),
		)
		return true
	}

	return false
}

func (c *ConnectorCore) CheckAndUpdateSocket(ctx context.Context, apiSocket, localSocket models.Socket) (*models.Socket, error) {
	if c.shouldUpdateSocket(apiSocket, localSocket) {
		apiSocket.AllowedEmailAddresses = localSocket.AllowedEmailAddresses
		apiSocket.AllowedEmailDomains = localSocket.AllowedEmailDomains
		apiSocket.UpstreamHttpHostname = localSocket.UpstreamHttpHostname
		apiSocket.ConnectorAuthenticationEnabled = localSocket.ConnectorAuthenticationEnabled
		apiSocket.UpstreamType = localSocket.UpstreamType
		apiSocket.CloudAuthEnabled = true
		apiSocket.Tags = localSocket.Tags
		*apiSocket.UpstreamPassword = ""
		*apiSocket.UpstreamUsername = ""

		_, err := NewPolicyManager(c.logger, c.border0API).ApplyPolicies(ctx, apiSocket, localSocket.PolicyNames)
		if err != nil {
			c.logger.Error(err.Error(), zap.String("socket_name", apiSocket.Name))
		}

		apiSocket.PolicyNames = localSocket.PolicyNames

		err = c.border0API.UpdateSocket(ctx, apiSocket.SocketID, apiSocket)
		if err != nil {
			c.logger.Error("error updating socket", zap.Error(err), zap.String("socket_name", apiSocket.Name))
			return nil, err
		}

		c.logger.Info("socket updated from local to api", zap.String("socket_name", apiSocket.Name))
	}

	return &apiSocket, nil
}

func (c *ConnectorCore) RecreateSocket(ctx context.Context, socketID string, localSocket models.Socket) (*models.Socket, error) {
	err := c.border0API.DeleteSocket(ctx, socketID)
	if err != nil {
		return nil, err
	}

	createdSocket, err := c.CreateSocket(ctx, &localSocket)
	if err != nil {
		return nil, err
	}

	createdSocket.BuildConnectorDataByTags()
	return createdSocket, nil
}

func (c *ConnectorCore) CheckSocketsToDelete(ctx context.Context, socketApiMap map[string]models.Socket, localSocketsMap map[string]models.Socket) error {
	for _, apiSocket := range socketApiMap {
		//skip not connector sockets
		if apiSocket.ConnectorData != nil && apiSocket.ConnectorData.Key() == "" {
			continue
		}

		if s, ok := localSocketsMap[apiSocket.ConnectorData.Key()]; ok {
			// check if socket needs to be recreated
			if *s.ConnectorData != *apiSocket.ConnectorData {
				c.logger.Info("socket data is different, so we are recreating the socket",
					zap.String("plugin_name", c.discovery.Name()),
					zap.Any("local connector data", apiSocket.ConnectorData),
					zap.Any("connector data", s.ConnectorData),
				)

				createdSocket, err := c.RecreateSocket(ctx, apiSocket.SocketID, s)
				if err != nil {
					return err
				}
				localSocketsMap[apiSocket.ConnectorData.Key()] = *createdSocket
				delete(socketApiMap, apiSocket.ConnectorData.Key())
				socketApiMap[createdSocket.ConnectorData.Key()] = *createdSocket
			}
		} else if apiSocket.ConnectorData.Connector == c.cfg.Connector.Name && apiSocket.ConnectorData.PluginName == c.discovery.Name() {
			c.logger.Info("socket does not exists locally, deleting the socket ",
				zap.String("plugin_name", c.discovery.Name()),
				zap.String("name", apiSocket.Name),
				zap.String("key", apiSocket.ConnectorData.Key()))

			// close tunnel connection before deleting the socket
			c.connectChan <- connectTunnelData{
				key:    apiSocket.SocketID,
				socket: apiSocket,
				action: "disconnect"}

			err := c.border0API.DeleteSocket(ctx, apiSocket.SocketID)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *ConnectorCore) CheckSocketsToCreate(ctx context.Context, localSockets []models.Socket, socketsFromApiMap map[string]models.Socket) ([]models.Socket, error) {
	var socketsToConnect []models.Socket

	for _, localSocket := range localSockets {
		if apiSocket, ok := socketsFromApiMap[localSocket.ConnectorData.Key()]; !ok {
			log.Printf("creating a socket: %s", localSocket.Name)

			createdSocket, err := c.CreateSocket(ctx, &localSocket)
			if err != nil {
				return nil, err
			}

			createdSocket.PluginName = c.discovery.Name()
			createdSocket.BuildConnectorData(c.cfg.Connector.Name, c.metadata)
			createdSocket.ConnectorLocalData = localSocket.ConnectorLocalData

			socketsToConnect = append(socketsToConnect, *createdSocket)
		} else {
			updatedSocket, err := c.CheckAndUpdateSocket(ctx, apiSocket, localSocket)
			if err != nil {
				c.logger.Info("error updating the socket", zap.String("error", err.Error()))
				return nil, err
			}

			updatedSocket.ConnectorLocalData = localSocket.ConnectorLocalData

			socketsToConnect = append(socketsToConnect, *updatedSocket)
		}
	}
	return socketsToConnect, nil
}

func (c *ConnectorCore) CreateSocket(ctx context.Context, s *models.Socket) (*models.Socket, error) {
	if s.Description == "" {
		s.Description = fmt.Sprintf("created by %s", c.cfg.Connector.Name)
	}

	//remove sensitive data
	s.UpstreamUsername = nil
	s.UpstreamPassword = nil
	s.UpstreamCert = nil
	s.UpstreamKey = nil
	s.UpstreamCa = nil

	createdSocket, err := c.border0API.CreateSocket(ctx, s)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	NewPolicyManager(c.logger, c.border0API).ApplyPolicies(ctx, *createdSocket, s.PolicyNames)
	createdSocket.PolicyNames = s.PolicyNames

	return createdSocket, nil
}

func (c *ConnectorCore) hostkey() (*gossh.Signer, error) {
	c.sshPrivateHostKeyLock.Lock()
	defer c.sshPrivateHostKeyLock.Unlock()

	if c.sshPrivateHostKey != nil {
		return c.sshPrivateHostKey, nil
	}

	hostkeySigner, err := util.Hostkey()
	if err != nil {
		if hostkeySigner == nil {
			return nil, fmt.Errorf("failed to get hostkey: %s", err)
		} else {
			c.logger.Warn("failed to store hostkey", zap.Error(err))
		}
	}

	c.sshPrivateHostKey = hostkeySigner

	return c.sshPrivateHostKey, nil

}

func (c *ConnectorCore) certificate(ctx context.Context, orgID string) (*tls.Certificate, error) {
	c.connectorCertificateLock.Lock()
	defer c.connectorCertificateLock.Unlock()

	if c.connectorCertificate != nil {
		return c.connectorCertificate, nil
	}

	certificate, err := util.GetEndToEndEncryptionCertificate(orgID, "")
	if err != nil {
		return nil, fmt.Errorf("failed to get connector certificate: %w", err)
	}

	if certificate == nil {
		_, privKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("failed to generate private key: %w", err)
		}

		csrTemplate := x509.CertificateRequest{
			Subject:            pkix.Name{CommonName: "border0"},
			SignatureAlgorithm: x509.PureEd25519,
		}

		csrBytes, err := x509.CreateCertificateRequest(rand.Reader, &csrTemplate, privKey)
		if err != nil {
			return nil, fmt.Errorf("failed to create certificate request: %w", err)
		}

		csrPem := pem.Block{
			Type:  "CERTIFICATE REQUEST",
			Bytes: csrBytes,
		}

		var name string
		hostname, err := os.Hostname()
		if err != nil {
			name = "border0-cli"
		} else {
			name = hostname
		}

		cert, err := c.border0API.ServerOrgCertificate(ctx, name, pem.EncodeToMemory(&csrPem))
		if err != nil {
			return nil, fmt.Errorf("failed to get certificate: %w", err)
		}

		privKeyBytes, err := x509.MarshalPKCS8PrivateKey(privKey)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal private key: %w", err)
		}

		privKeyPem := &pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: privKeyBytes,
		}

		tlsCert, err := tls.X509KeyPair(cert, pem.EncodeToMemory(privKeyPem))
		if err != nil {
			return nil, fmt.Errorf("failed to parse certificate: %w", err)
		}

		c.connectorCertificate = &tlsCert

		if err := util.StoreConnectorCertifcate(privKey, cert, orgID, ""); err != nil {
			c.logger.Warn("failed to store certificate", zap.Error(err))
		}
	}

	return c.connectorCertificate, nil
}

func stringSlicesEqual(a, b []string) bool {
	sort.Strings(a)
	sort.Strings(b)

	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

func StringInSlice(s string, list []string) bool {
	for _, x := range list {
		if s == x {
			return true
		}
	}
	return false
}
