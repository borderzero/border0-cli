package connectorv2

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/gob"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"os/user"
	"strings"
	"sync"
	"time"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/borderzero/border0-cli/internal/connector_v2/cmds"
	"github.com/borderzero/border0-cli/internal/connector_v2/config"
	"github.com/borderzero/border0-cli/internal/connector_v2/errors"
	"github.com/borderzero/border0-cli/internal/connector_v2/logger"
	"github.com/borderzero/border0-cli/internal/connector_v2/plugin"
	"github.com/borderzero/border0-cli/internal/connector_v2/upstreamdata"
	"github.com/borderzero/border0-cli/internal/connector_v2/util"
	"github.com/borderzero/border0-cli/internal/sqlauthproxy"
	"github.com/borderzero/border0-cli/internal/ssh"
	sshConfig "github.com/borderzero/border0-cli/internal/ssh/config"
	"github.com/borderzero/border0-cli/internal/ssh/server"
	"github.com/borderzero/border0-go/lib/types/set"
	"github.com/borderzero/border0-go/types/connector"
	"github.com/borderzero/border0-go/types/service"
	pb "github.com/borderzero/border0-proto/connector"
	backoff "github.com/cenkalti/backoff/v4"
	"github.com/golang-jwt/jwt"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	gossh "golang.org/x/crypto/ssh"
)

const (
	backoffMaxInterval       = 1 * time.Hour
	serviceConfigPath        = "/etc/border0/"
	sshHostKeyFile           = "ssh_host_ecdsa_key"
	connectorCertificateFile = "connector.crt"
	connectorPrivateKeyFile  = "connector.key"
)

type ConnectorService struct {
	config                   *config.Configuration
	logger                   *zap.Logger
	backoff                  *backoff.ExponentialBackOff
	version                  string
	context                  context.Context
	stream                   pb.ConnectorService_ControlStreamClient
	heartbeatInterval        int
	plugins                  map[string]plugin.Plugin
	sockets                  map[string]*border0.Socket
	requests                 sync.Map
	organization             *models.Organization
	discoveryResultChan      chan *plugin.PluginDiscoveryResults
	sshPrivateHostKey        *gossh.Signer
	sshPrivateHostKeyLock    sync.Mutex
	connectorCertificateLock sync.Mutex
	connectorCertificate     *tls.Certificate
}

func NewConnectorService(
	ctx context.Context,
	l *zap.Logger,
	version string,
	config *config.Configuration,
) *ConnectorService {
	cs := &ConnectorService{
		config:              config,
		version:             version,
		context:             ctx,
		heartbeatInterval:   10,
		plugins:             make(map[string]plugin.Plugin),
		sockets:             make(map[string]*border0.Socket),
		discoveryResultChan: make(chan *plugin.PluginDiscoveryResults, 100),
	}

	cs.logger = logger.NewConnectorLogger(l, cs.sendControlStreamRequest)
	return cs
}

func (c *ConnectorService) Start() {
	c.logger.Info("starting the connector service")
	newCtx, cancel := context.WithCancel(c.context)

	go c.StartControlStream(newCtx, cancel)
	go c.handleDiscoveryResult(newCtx)

	<-newCtx.Done()
}

func (c *ConnectorService) StartControlStream(ctx context.Context, cancel context.CancelFunc) {
	defer cancel()

	c.backoff = backoff.NewExponentialBackOff()
	c.backoff.MaxElapsedTime = 0
	c.backoff.MaxInterval = backoffMaxInterval

	if err := backoff.Retry(c.controlStream, c.backoff); err != nil {
		c.logger.Error("error in control stream", zap.Error(err))
	}
}

func (c *ConnectorService) heartbeat(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(c.heartbeatInterval) * time.Second):
			if err := c.sendControlStreamRequest(&pb.ControlStreamRequest{RequestType: &pb.ControlStreamRequest_Heartbeat{Heartbeat: &pb.HeartbeatRequest{}}}); err != nil {
				c.logger.Error("failed to send heartbeat", zap.Error(err))
			}
		}
	}
}

func (c *ConnectorService) controlStream() error {
	ctx, cancel := context.WithCancel(c.context)
	defer cancel()

	defer func() {
		c.logger.Debug("control stream closed", zap.Duration("next retry", c.backoff.NextBackOff()))
	}()

	grpcConn, err := c.newConnectorClient(ctx)
	if err != nil {
		c.logger.Error("failed to setup connection", zap.Error(err))
		return fmt.Errorf("failed to create connector client: %w", err)
	}

	defer grpcConn.Close()

	stream, err := pb.NewConnectorServiceClient(grpcConn).ControlStream(c.context)
	if err != nil {
		c.logger.Error("failed to setup control stream", zap.Error(err))
		return fmt.Errorf("failed to create control stream: %w", err)
	}

	c.stream = stream

	defer func() { c.stream = nil }()
	go c.heartbeat(ctx)
	go c.uploadConnectorMetadata(ctx)

	errChan := make(chan error)
	msgChan := make(chan struct {
		response *pb.ControlStreamReponse
		error    error
	})

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				msg, err := stream.Recv()
				msgChan <- struct {
					response *pb.ControlStreamReponse
					error    error
				}{msg, err}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			err := stream.CloseSend()
			if err != nil {
				return backoff.Permanent(fmt.Errorf("failed to close control stream: %w", err))
			}

			return nil
		case err := <-errChan:
			return err
		case msg := <-msgChan:
			if msg.error != nil {
				statusErr, ok := status.FromError(msg.error)
				if ok && statusErr.Code() == codes.Canceled && statusErr.Message() == "connector deleted" {
					return backoff.Permanent(fmt.Errorf("connector was deleted"))
				}

				c.logger.Error("failed to receive message", zap.Error(msg.error))
				return msg.error
			}

			go func() {
				switch r := msg.response.GetRequestType().(type) {
				case *pb.ControlStreamReponse_ConnectorConfig:
					if err := c.handleConnectorConfig(r.ConnectorConfig); err != nil {
						c.logger.Error("Failed to handle connector config", zap.Error(err))
					}
				case *pb.ControlStreamReponse_Init:
					if err := c.handleInit(r.Init); err != nil {
						c.logger.Error("Failed to handle init", zap.Error(err))
						errChan <- fmt.Errorf("failed to handle init: %w", err)
					}
				case *pb.ControlStreamReponse_UpdateConfig:
					switch t := r.UpdateConfig.GetConfigType().(type) {
					case *pb.UpdateConfig_PluginConfig:
						if err := c.handlePluginConfig(r.UpdateConfig.GetAction(), r.UpdateConfig.GetPluginConfig()); err != nil {
							c.logger.Error("Failed to handle plugin config", zap.Error(err))
							errChan <- fmt.Errorf("failed to handle plugin config: %w", err)
						}
					case *pb.UpdateConfig_SocketConfig:
						if err := c.handleSocketConfig(r.UpdateConfig.GetAction(), r.UpdateConfig.GetSocketConfig()); err != nil {
							c.logger.Error("Failed to handle socket config", zap.Error(err))
							errChan <- fmt.Errorf("failed to handle socket config: %w", err)
						}
					default:
						c.logger.Error("unknown config type", zap.Any("type", t))
					}
				case *pb.ControlStreamReponse_TunnelCertificateSignResponse:
					if v, ok := c.requests.Load(r.TunnelCertificateSignResponse.GetRequestId()); ok {
						responseChan, ok := v.(chan *pb.ControlStreamReponse)
						if !ok {
							c.logger.Error("failed to cast response channel", zap.String("request_id", r.TunnelCertificateSignResponse.GetRequestId()))
						}
						select {
						case responseChan <- msg.response:
						default:
							c.logger.Error("failed to send response to request channel", zap.String("request_id", r.TunnelCertificateSignResponse.GetRequestId()))
						}
					} else {
						c.logger.Error("unknown request id", zap.String("request_id", r.TunnelCertificateSignResponse.GetRequestId()))
					}
				case *pb.ControlStreamReponse_SshCertificateSignResponse:
					if v, ok := c.requests.Load(r.SshCertificateSignResponse.GetRequestId()); ok {
						responseChan, ok := v.(chan *pb.ControlStreamReponse)
						if !ok {
							c.logger.Error("failed to cast response channel", zap.String("request_id", r.SshCertificateSignResponse.GetRequestId()))
						}
						select {
						case responseChan <- msg.response:
						default:
							c.logger.Error("failed to send response to request channel", zap.String("request_id", r.SshCertificateSignResponse.GetRequestId()))
						}
					} else {
						c.logger.Error("unknown request id", zap.String("request_id", r.SshCertificateSignResponse.GetRequestId()))
					}
				case *pb.ControlStreamReponse_Heartbeat:
				case *pb.ControlStreamReponse_Stop:
					c.logger.Info("stopping connector as requested by server")
					errChan <- backoff.Permanent(nil)
				case *pb.ControlStreamReponse_Disconnect:
					c.logger.Info("disconnecting connector as requested by server")
					err := stream.CloseSend()
					if err != nil {
						errChan <- fmt.Errorf("failed to close control stream: %w", err)
					}
					errChan <- fmt.Errorf("connector was disconnected by server")
				case *pb.ControlStreamReponse_Authorize:
					if v, ok := c.requests.Load(r.Authorize.GetRequestId()); ok {
						responseChan, ok := v.(chan *pb.ControlStreamReponse)
						if !ok {
							c.logger.Error("failed to cast response channel", zap.String("request_id", r.Authorize.GetRequestId()))
						}
						select {
						case responseChan <- msg.response:
						default:
							c.logger.Error("failed to send response to request channel", zap.String("request_id", r.Authorize.GetRequestId()))
						}
					} else {
						c.logger.Error("unknown request id", zap.String("request_id", r.Authorize.RequestId))
					}
				case *pb.ControlStreamReponse_CertifcateSignResponse:
					if v, ok := c.requests.Load(r.CertifcateSignResponse.GetRequestId()); ok {
						responseChan, ok := v.(chan *pb.ControlStreamReponse)
						if !ok {
							c.logger.Error("failed to cast response channel", zap.String("request_id", r.CertifcateSignResponse.GetRequestId()))
						}
						select {
						case responseChan <- msg.response:
						default:
							c.logger.Error("failed to send response to request channel", zap.String("request_id", r.CertifcateSignResponse.RequestId))
						}
					} else {
						c.logger.Error("unknown request id", zap.String("request_id", r.CertifcateSignResponse.GetRequestId()))
					}
				default:
					c.logger.Error("unknown message type", zap.Any("type", r))
				}
			}()
		}
	}
}

func (c *ConnectorService) newConnectorClient(ctx context.Context) (*grpc.ClientConn, error) {
	ccsOpts := []CredentialOption{
		WithToken(c.config.Token),
		WithInsecureTransport(c.config.ConnectorInsecureTransport),
	}
	if c.config.ConnectorId != "" {
		ccsOpts = append(ccsOpts, WithConnectorId(c.config.ConnectorId))
	}

	grpcOpts := []grpc.DialOption{
		grpc.WithPerRPCCredentials(NewConnectorControlStreamCredentials(ccsOpts...)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                20 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	}

	if c.config.ConnectorInsecureTransport {
		grpcOpts = append(grpcOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		grpcOpts = append(grpcOpts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})))
	}

	c.logger.Info("connecting to connector server", zap.String("server", c.config.ConnectorServer))
	client, err := grpc.DialContext(c.context, c.config.ConnectorServer, grpcOpts...)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func (c *ConnectorService) handleConnectorConfig(config *pb.ConnectorConfig) error {
	c.heartbeatInterval = int(config.HeartbeatInterval)
	return nil
}

func (c *ConnectorService) handleInit(init *pb.Init) error {
	connectorConfig := init.GetConnectorConfig()
	pluginConfig := init.GetPlugins()
	socketConfg := init.GetSockets()

	if connectorConfig == nil {
		return fmt.Errorf("init message is missing required fields")
	}

	c.heartbeatInterval = int(connectorConfig.GetHeartbeatInterval())

	certificates := make(map[string]string)
	if err := util.AsStruct(connectorConfig.Organization.Certificates, &certificates); err != nil {
		return fmt.Errorf("failed to parse organization certificates: %w", err)
	}

	c.organization = &models.Organization{
		Certificates: certificates,
	}

	knownPlugins := set.New[string]()

	for _, config := range pluginConfig {
		var action pb.Action
		if _, ok := c.plugins[config.GetId()]; ok {
			action = pb.Action_UPDATE
		} else {
			action = pb.Action_CREATE
		}
		if err := c.handlePluginConfig(action, config); err != nil {
			return fmt.Errorf("failed to handle plugin config: %w", err)
		}
		knownPlugins.Add(config.GetId())
	}
	for id := range c.plugins {
		if !knownPlugins.Has(id) {
			if err := c.handlePluginConfig(pb.Action_DELETE, &pb.PluginConfig{Id: id}); err != nil {
				return fmt.Errorf("failed to handle plugin config: %w", err)
			}
		}
	}

	knownSockets := set.New[string]()

	for _, config := range socketConfg {
		var action pb.Action
		if _, ok := c.sockets[config.GetId()]; ok {
			action = pb.Action_UPDATE
		} else {
			action = pb.Action_CREATE
		}
		if err := c.handleSocketConfig(action, config); err != nil {
			return fmt.Errorf("failed to handle socket config: %w", err)
		}
		knownSockets.Add(config.GetId())
	}
	for id := range c.sockets {
		if !knownSockets.Has(id) {
			if err := c.handleSocketConfig(pb.Action_DELETE, &pb.SocketConfig{Id: id}); err != nil {
				return fmt.Errorf("failed to handle socket config: %w", err)
			}
		}
	}

	c.backoff.Reset()

	return nil
}

func (c *ConnectorService) handlePluginConfig(action pb.Action, config *pb.PluginConfig) error {
	var innerConfig *connector.PluginConfiguration
	if err := util.AsStruct(config.GetConfig(), &innerConfig); err != nil {
		return fmt.Errorf("failed to decode plugin configuration: %v", err)
	}

	switch action {
	case pb.Action_CREATE:
		c.logger.Info("new plugin", zap.String("plugin", config.GetId()))

		if _, ok := c.plugins[config.GetId()]; ok {
			return fmt.Errorf("plugin already exists")
		}

		p, err := plugin.NewPlugin(c.context, c.logger, config.GetId(), config.GetType(), innerConfig)
		if err != nil {
			return fmt.Errorf("failed to register plugin: %w", err)
		}

		go p.Start(c.context, c.discoveryResultChan)

		c.plugins[config.GetId()] = p
	case pb.Action_UPDATE:
		c.logger.Info("update plugin", zap.String("plugin", config.GetId()))

		p, ok := c.plugins[config.GetId()]
		if !ok {
			return fmt.Errorf("plugin does not exist")
		}

		if err := p.Stop(); err != nil {
			return fmt.Errorf("failed to stop plugin: %w", err)
		}

		p, err := plugin.NewPlugin(c.context, c.logger, config.GetId(), config.GetType(), innerConfig)
		if err != nil {
			return fmt.Errorf("failed to register plugin: %w", err)
		}

		go p.Start(c.context, c.discoveryResultChan)

		c.plugins[config.GetId()] = p
	case pb.Action_DELETE:
		c.logger.Info("delete plugin", zap.String("plugin", config.GetId()))

		p, ok := c.plugins[config.GetId()]
		if !ok {
			return fmt.Errorf("plugin does not exists")
		}

		if err := p.Stop(); err != nil {
			return fmt.Errorf("failed to delete plugin: %w", err)
		}

		delete(c.plugins, config.GetId())
	default:
		return fmt.Errorf("unknown action: %s", action)
	}

	return nil
}

func (c *ConnectorService) handleSocketConfig(action pb.Action, config *pb.SocketConfig) error {
	switch action {
	case pb.Action_CREATE:
		c.logger.Info("new socket", zap.String("socket", config.GetId()))

		if _, ok := c.sockets[config.GetId()]; ok {
			return fmt.Errorf("socket already exists")
		}

		socket, err := c.newSocket(config)
		if err != nil {
			return fmt.Errorf("failed to create socket: %w", err)
		}

		c.sockets[config.GetId()] = socket
	case pb.Action_UPDATE:
		c.logger.Info("update socket", zap.String("socket", config.GetId()))

		socket, ok := c.sockets[config.GetId()]
		if !ok {
			return fmt.Errorf("socket does not exist")
		}

		var connectorSocketConfig service.ConnectorServiceConfiguration
		if err := util.AsStruct(config.GetConfig(), &connectorSocketConfig); err != nil {
			return fmt.Errorf("failed to parse socket config: %w", err)
		}

		newHash, err := hashStruct(connectorSocketConfig)
		if err != nil {
			return fmt.Errorf("failed to hash socket config: %w", err)
		}

		if socket.ConfigHash == newHash {
			return nil
		}

		if !socket.IsClosed() {
			socket.Close()
		}

		socket, err = c.newSocket(config)
		if err != nil {
			return fmt.Errorf("failed to create socket: %w", err)
		}

		c.sockets[config.GetId()] = socket
	case pb.Action_DELETE:
		c.logger.Info("delete socket", zap.String("socket", config.GetId()))

		socket, ok := c.sockets[config.GetId()]
		if !ok {
			return fmt.Errorf("socket does not exists")
		}

		if !socket.IsClosed() {
			socket.Close()
		}

		delete(c.sockets, config.GetId())
	default:
		return fmt.Errorf("unknown action: %s", action)
	}

	return nil
}

func (c *ConnectorService) newSocket(config *pb.SocketConfig) (*border0.Socket, error) {
	var connectorSocketConfig service.ConnectorServiceConfiguration
	if err := util.AsStruct(config.GetConfig(), &connectorSocketConfig); err != nil {
		return nil, fmt.Errorf("failed to parse socket config: %w", err)
	}

	s := &models.Socket{
		SocketID:                       config.GetId(),
		SocketType:                     config.GetType(),
		ConnectorAuthenticationEnabled: connectorSocketConfig.ConnectorAuthenticationEnabled,
		EndToEndEncryptionEnabled:      connectorSocketConfig.EndToEndEncryptionEnabled,
		RecordingEnabled:               connectorSocketConfig.RecordingEnabled,
	}

	if s.ConnectorLocalData == nil {
		s.ConnectorLocalData = &models.ConnectorLocalData{}
	}

	if s.ConnectorData == nil {
		s.ConnectorData = &models.ConnectorData{}
	}

	if err := upstreamdata.NewUpstreamDataBuilder(c.logger).Build(s, connectorSocketConfig.Upstream); err != nil {
		return nil, fmt.Errorf("failed to build upstream data: %w", err)
	}

	var certificate *tls.Certificate
	if s.EndToEndEncryptionEnabled {
		var err error
		certificate, err = c.Certificate()
		if err != nil {
			return nil, fmt.Errorf("failed to get connector certificate: %w", err)
		}
	}

	socket, err := border0.NewSocketFromConnectorAPI(c.context, c, *s, c.organization, c.logger.With(zap.String("socket_id", s.SocketID)), certificate)
	if err != nil {
		return nil, fmt.Errorf("failed to create socket: %w", err)
	}

	if socket.ConfigHash, err = hashStruct(connectorSocketConfig); err != nil {
		return nil, fmt.Errorf("failed to hash socket config: %w", err)
	}

	go c.Listen(socket)

	return socket, nil
}

func (c *ConnectorService) GetUserID() (string, error) {
	token, _ := jwt.Parse(c.config.Token, nil)
	if token == nil {
		return "", fmt.Errorf("failed to parse token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", fmt.Errorf("failed to parse token")
	}

	connectorId, connectorIdPresent := claims["connector_id"]
	if connectorIdPresent {
		connectorIdStr, ok := connectorId.(string)
		if !ok {
			return "", fmt.Errorf("failed to parse token")
		}
		return strings.ReplaceAll(connectorIdStr, "-", ""), nil
	}

	if c.config.ConnectorId != "" {
		return strings.ReplaceAll(c.config.ConnectorId, "-", ""), nil
	}

	return "", fmt.Errorf("failed to get user id")
}

func (c *ConnectorService) SignSSHKey(ctx context.Context, socketID string, publicKey []byte) (string, string, error) {
	requestId := uuid.New().String()
	if err := c.sendControlStreamRequest(&pb.ControlStreamRequest{
		RequestType: &pb.ControlStreamRequest_TunnelCertificateSignRequest{
			TunnelCertificateSignRequest: &pb.TunnelCertificateSignRequest{
				RequestId: requestId,
				SocketId:  socketID,
				PublicKey: string(publicKey),
			},
		},
	}); err != nil {
		return "", "", fmt.Errorf("failed to send tunnel certificate sign request: %w", err)
	}

	recChan := make(chan *pb.ControlStreamReponse)
	c.requests.Store(requestId, recChan)

	select {
	case <-time.After(5 * time.Second):
		return "", "", fmt.Errorf("timeout waiting for tunnel certificate sign response")
	case r := <-recChan:
		response := r.GetTunnelCertificateSignResponse()
		if response == nil {
			return "", "", fmt.Errorf("invalid response")
		}

		if response.GetRequestId() == "" {
			return "", "", fmt.Errorf("invalid response")
		}

		c.requests.Delete(response.GetRequestId())
		return response.GetCertificate(), response.GetHostkey(), nil
	}
}

func (c *ConnectorService) Listen(socket *border0.Socket) {
	logger := c.logger.With(zap.String("socket_id", socket.SocketID))

	l, err := socket.Listen()
	if err != nil {
		logger.Error("failed to start listener", zap.String("socket", socket.SocketID), zap.Error(err))
		return
	}

	defer l.Close()

	var handlerConfig *sqlauthproxy.Config
	if socket.SocketType == "database" {
		handlerConfig, err = sqlauthproxy.BuildHandlerConfig(logger, *socket.Socket, c)
		if err != nil {
			logger.Error("failed to create config for socket", zap.String("socket", socket.SocketID), zap.Error(err))
			return
		}
	}

	var sshProxyConfig *sshConfig.ProxyConfig
	if socket.SocketType == "ssh" {
		var hostkeySigner *gossh.Signer
		if socket.EndToEndEncryptionEnabled {
			hostkeySigner, err = c.hostkey()
			if err != nil {
				logger.Error("failed to get hostkey", zap.String("socket", socket.SocketID), zap.Error(err))
				return
			}
		}

		sshProxyConfig, err = sshConfig.BuildProxyConfig(logger, *socket.Socket, socket.Socket.AWSRegion, "", hostkeySigner, c.organization, c)
		if err != nil {
			logger.Error("failed to create config for socket", zap.String("socket", socket.SocketID), zap.Error(err))
			return
		}
	}

	switch {
	case socket.Socket.SSHServer && socket.SocketType == "ssh" && !socket.EndToEndEncryptionEnabled:
		opts := []server.Option{}
		if socket.Socket != nil &&
			socket.Socket.ConnectorLocalData != nil &&
			socket.Socket.ConnectorLocalData.UpstreamUsername != "" {
			opts = append(opts, server.WithUsername(socket.Socket.ConnectorLocalData.UpstreamUsername))
		}

		sshServer, err := server.NewServer(logger, c.organization.Certificates["ssh_public_key"], opts...)
		if err != nil {
			logger.Error("failed to create ssh server", zap.String("socket", socket.SocketID), zap.Error(err))
			return
		}
		if err := sshServer.Serve(l); err != nil {
			logger.Error("ssh server failed", zap.String("socket", socket.SocketID), zap.Error(err))
		}
	case sshProxyConfig != nil:
		if err := ssh.Proxy(l, *sshProxyConfig); err != nil {
			logger.Error("ssh proxy failed", zap.String("socket", socket.SocketID), zap.Error(err))
		}
	case handlerConfig != nil:
		if err := sqlauthproxy.Serve(l, *handlerConfig); err != nil {
			logger.Error("sql proxy failed", zap.String("socket", socket.SocketID), zap.Error(err))
		}
	default:
		if err := border0.Serve(logger, l, socket.Socket.TargetHostname, socket.Socket.TargetPort); err != nil {
			logger.Error("proxy failed", zap.String("socket", socket.SocketID), zap.Error(err))
		}
	}
}

func (c *ConnectorService) handleDiscoveryResult(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case result := <-c.discoveryResultChan:
			var resources []*structpb.Struct
			for _, r := range result.Result.Resources {
				var pbstruct structpb.Struct
				if err := util.AsPbStruct(r, &pbstruct); err != nil {
					c.logger.Error("failed to convert go struct to pb struct", zap.Error(err))
					continue
				}
				resources = append(resources, &pbstruct)
			}

			if err := c.sendControlStreamRequest(&pb.ControlStreamRequest{
				RequestType: &pb.ControlStreamRequest_PluginDiscoveryResults{
					PluginDiscoveryResults: &pb.PluginDiscoveryResults{
						PluginId: result.PluginID,
						Metadata: &pb.PluginDiscoveryResultsMetadata{
							DiscoveryId: result.Result.Metadata.DiscovererId,
							StartedAt:   timestamppb.New(result.Result.Metadata.StartedAt),
							EndedAt:     timestamppb.New(result.Result.Metadata.EndedAt),
						},
						Errors:    result.Result.Errors,
						Warnings:  result.Result.Warnings,
						Resources: resources,
					},
				},
			}); err != nil {
				c.logger.Error("failed to send plugin discovery results", zap.Error(err))
				continue
			}
		}
	}
}

func (c *ConnectorService) uploadConnectorMetadata(ctx context.Context) {
	metadata := cmds.MetadataFromContext(ctx)
	c.logger.Debug("collected connector metadata", zap.Any("metadata", metadata))

	var pbstruct structpb.Struct
	if err := util.AsPbStruct(metadata, &pbstruct); err != nil {
		c.logger.Error("failed to convert go struct to pb struct", zap.Error(err))
		return
	}

	err := c.sendControlStreamRequest(&pb.ControlStreamRequest{
		RequestType: &pb.ControlStreamRequest_Metadata{
			Metadata: &pb.ConnectorMetadata{
				Data: &pbstruct,
			},
		},
	})
	if err != nil {
		c.logger.Error("failed to send connector metadata", zap.Error(err))
		return
	}
}

func (c *ConnectorService) sendControlStreamRequest(request *pb.ControlStreamRequest) error {
	if c.stream == nil {
		return fmt.Errorf(errors.ErrStreamNotConnected)
	}

	return c.stream.Send(request)
}

func hashStruct(data interface{}) (string, error) {
	var buf bytes.Buffer

	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(data); err != nil {
		return "", err
	}

	hash := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(hash[:]), nil
}

func (c *ConnectorService) hostkey() (*gossh.Signer, error) {
	c.sshPrivateHostKeyLock.Lock()
	defer c.sshPrivateHostKeyLock.Unlock()

	if c.sshPrivateHostKey != nil {
		return c.sshPrivateHostKey, nil
	}

	var keyFilePath string
	if _, err := os.Stat(serviceConfigPath + sshHostKeyFile); err == nil {
		keyFilePath = serviceConfigPath + sshHostKeyFile
	} else {
		u, err := user.Current()
		if err == nil {
			if _, err := os.Stat(u.HomeDir + "/.border0/" + sshHostKeyFile); err == nil {
				keyFilePath = u.HomeDir + "/.border0/" + sshHostKeyFile
			}
		}
	}

	if keyFilePath != "" {
		keyBytes, err := os.ReadFile(keyFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read host key %s: %w", keyFilePath, err)
		}

		privateKey, err := gossh.ParsePrivateKey(keyBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse host key: %w", err)
		}

		c.sshPrivateHostKey = &privateKey
		return c.sshPrivateHostKey, nil
	}

	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate host key: %w", err)
	}

	sshPrivKey, err := gossh.NewSignerFromKey(privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to convert ed25519 key to ssh key: %w", err)
	}

	c.sshPrivateHostKey = &sshPrivKey

	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal private key to PKCS#8: %w", err)
	}

	privKeyPEM := &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: pkcs8Bytes,
	}

	serviceConfigPathErr := storeHostkey(pem.EncodeToMemory(privKeyPEM), serviceConfigPath, sshHostKeyFile)
	if serviceConfigPathErr != nil {
		u, err := user.Current()
		if err != nil {
			return nil, fmt.Errorf("failed to store the ssh hostkey file %w %w", err, serviceConfigPathErr)
		}

		err = storeHostkey(pem.EncodeToMemory(privKeyPEM), u.HomeDir+"/.border0/", sshHostKeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to store the ssh hostkey file %w %w", err, serviceConfigPathErr)
		}
	}

	return c.sshPrivateHostKey, nil
}

func storeHostkey(key []byte, path, filename string) error {
	if _, err := os.Stat(path); err == nil {
		if err := os.MkdirAll(path, 0700); err != nil {
			return fmt.Errorf("failed to create directory %s %w", path, err)
		}
	}

	if err := os.WriteFile(path+filename, key, 0600); err != nil {
		return fmt.Errorf("failed to write host key: %w", err)
	}

	return nil
}

func (c *ConnectorService) Certificate() (*tls.Certificate, error) {
	c.connectorCertificateLock.Lock()
	defer c.connectorCertificateLock.Unlock()

	if c.connectorCertificate != nil {
		return c.connectorCertificate, nil
	}

	var keyFilePath, certFilePath string

	if _, err := os.Stat(serviceConfigPath + connectorPrivateKeyFile); err == nil {
		keyFilePath = serviceConfigPath + connectorPrivateKeyFile
	}

	if _, err := os.Stat(serviceConfigPath + connectorCertificateFile); err == nil {
		certFilePath = serviceConfigPath + connectorCertificateFile
	}

	if keyFilePath == "" || certFilePath == "" {
		u, err := user.Current()
		if err == nil {
			if _, err := os.Stat(u.HomeDir + "/.border0/" + connectorPrivateKeyFile); err == nil {
				keyFilePath = u.HomeDir + "/.border0/" + connectorPrivateKeyFile
			}

			if _, err := os.Stat(u.HomeDir + "/.border0/" + connectorCertificateFile); err == nil {
				certFilePath = u.HomeDir + "/.border0/" + connectorCertificateFile
			}
		}
	}

	if keyFilePath != "" && certFilePath != "" {
		certificate, err := tls.LoadX509KeyPair(certFilePath, keyFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load certificate: %s", err)
		}

		c.connectorCertificate = &certificate
		return c.connectorCertificate, nil
	}

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

	requestId := uuid.New().String()
	recChan := make(chan *pb.ControlStreamReponse)
	c.requests.Store(requestId, recChan)
	defer c.requests.Delete(requestId)

	if err := c.sendControlStreamRequest(&pb.ControlStreamRequest{
		RequestType: &pb.ControlStreamRequest_CertifcateSignRequest{
			CertifcateSignRequest: &pb.CertifcateSignRequest{
				RequestId:                 requestId,
				CertificateSigningRequest: pem.EncodeToMemory(&csrPem),
			},
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to send connector certificate sign request: %w", err)
	}

	var certificate []byte
	select {
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("timeout waiting for certificate sign response")
	case r := <-recChan:
		response := r.GetCertifcateSignResponse()
		if response == nil {
			return nil, fmt.Errorf("invalid response")
		}

		if response.GetRequestId() == "" {
			return nil, fmt.Errorf("invalid response")
		}

		c.requests.Delete(response.GetRequestId())
		certificate = response.GetCertificate()
	}

	privKeyBytes, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal private key: %w", err)
	}

	privKeyPem := &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privKeyBytes,
	}

	cert, err := tls.X509KeyPair(certificate, pem.EncodeToMemory(privKeyPem))
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	c.connectorCertificate = &cert

	serviceConfigPathErr := storeConnectorCertifcate(pem.EncodeToMemory(privKeyPem), certificate, serviceConfigPath, connectorPrivateKeyFile, connectorCertificateFile)
	if serviceConfigPathErr != nil {
		u, err := user.Current()
		if err != nil {
			return nil, fmt.Errorf("failed to store the certifcate files %s %s", err, serviceConfigPathErr)
		}

		err = storeConnectorCertifcate(pem.EncodeToMemory(privKeyPem), certificate, u.HomeDir+"/.border0/", connectorPrivateKeyFile, connectorCertificateFile)
		if err != nil {
			return nil, fmt.Errorf("failed to store the certifcate files %s %s", err, serviceConfigPathErr)
		}
	}

	return c.connectorCertificate, nil
}

func storeConnectorCertifcate(key []byte, certficate []byte, path, keyFileName, certificateFileName string) error {
	if _, err := os.Stat(path); err == nil {
		if err := os.MkdirAll(path, 0700); err != nil {
			return fmt.Errorf("failed to create directory %s %w", path, err)
		}
	}

	if err := os.WriteFile(path+keyFileName, key, 0600); err != nil {
		return fmt.Errorf("failed to write key file: %w", err)
	}

	if err := os.WriteFile(path+certificateFileName, certficate, 0600); err != nil {
		return fmt.Errorf("failed to write certificate file: %w", err)
	}

	return nil
}
