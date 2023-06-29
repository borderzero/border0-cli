package connectorv2

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/borderzero/border0-cli/internal/connector_v2/config"
	"github.com/borderzero/border0-cli/internal/connector_v2/plugin"
	"github.com/borderzero/border0-cli/internal/connector_v2/util"
	"github.com/borderzero/border0-cli/internal/sqlauthproxy"
	"github.com/borderzero/border0-cli/internal/ssh"
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
)

const (
	BackoffMaxInterval = 1 * time.Hour
)

type ConnectorService struct {
	config            *config.Configuration
	logger            *zap.Logger
	backoff           *backoff.ExponentialBackOff
	version           string
	context           context.Context
	stream            pb.ConnectorService_ControlStreamClient
	heartbeatInterval int
	plugins           map[string]plugin.Plugin
	sockets           map[string]*border0.Socket
	requests          map[string]chan *pb.ControlStreamReponse
	organization      *models.Organization
}

func NewConnectorService(ctx context.Context, logger *zap.Logger, version string) *ConnectorService {
	config, err := config.GetConfiguration(ctx)
	if err != nil {
		logger.Fatal("failed to get configuration", zap.Error(err))
	}

	return &ConnectorService{
		config:            config,
		logger:            logger,
		version:           version,
		context:           ctx,
		heartbeatInterval: 10,
		plugins:           make(map[string]plugin.Plugin),
		sockets:           make(map[string]*border0.Socket),
		requests:          make(map[string]chan *pb.ControlStreamReponse),
	}
}

func (c *ConnectorService) Start() {
	c.logger.Info("starting the connector service")
	newCtx, cancel := context.WithCancel(c.context)

	go c.StartControlStream(newCtx, cancel)

	<-newCtx.Done()
}

func (c *ConnectorService) StartControlStream(ctx context.Context, cancel context.CancelFunc) {
	defer cancel()

	c.backoff = backoff.NewExponentialBackOff()
	c.backoff.MaxElapsedTime = 0
	c.backoff.MaxInterval = BackoffMaxInterval

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
			if err := c.stream.Send(&pb.ControlStreamRequest{RequestType: &pb.ControlStreamRequest_Heartbeat{Heartbeat: &pb.HeartbeatRequest{}}}); err != nil {
				c.logger.Error("failed to send heartbeat", zap.Error(err))
			}
		}
	}
}

func (c *ConnectorService) controlStream() error {
	ctx, cancel := context.WithCancel(c.context)
	defer cancel()

	defer func() { c.logger.Debug("control stream closed", zap.Duration("next retry", c.backoff.NextBackOff())) }()

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

	for {
		msgChan := make(chan struct {
			response *pb.ControlStreamReponse
			error    error
		})

		go func() {
			msg, err := stream.Recv()
			msgChan <- struct {
				response *pb.ControlStreamReponse
				error    error
			}{msg, err}
		}()

		select {
		case <-ctx.Done():
			err := stream.CloseSend()
			if err != nil {
				return backoff.Permanent(fmt.Errorf("failed to close control stream: %w", err))
			}

			return nil
		case msg := <-msgChan:
			if msg.error != nil {
				statusErr, ok := status.FromError(msg.error)
				if ok && statusErr.Code() == codes.Canceled && statusErr.Message() == "connector deleted" {
					return backoff.Permanent(fmt.Errorf("connector was deleted"))
				}

				c.logger.Error("failed to receive message", zap.Error(msg.error))
				return msg.error
			}

			switch r := msg.response.GetRequestType().(type) {
			case *pb.ControlStreamReponse_ConnectorConfig:
				if err := c.handleConnectorConfig(r.ConnectorConfig); err != nil {
					c.logger.Error("Failed to handle connector config", zap.Error(err))
				}
			case *pb.ControlStreamReponse_Init:
				if err := c.handleInit(r.Init); err != nil {
					c.logger.Error("Failed to handle init", zap.Error(err))
					return fmt.Errorf("failed to handle init: %w", err)
				}
			case *pb.ControlStreamReponse_UpdateConfig:
				switch t := r.UpdateConfig.GetConfigType().(type) {
				case *pb.UpdateConfig_PluginConfig:
					if err := c.handlePluginConfig(r.UpdateConfig.GetAction(), r.UpdateConfig.GetPluginConfig()); err != nil {
						c.logger.Error("Failed to handle plugin config", zap.Error(err))
						return fmt.Errorf("failed to handle plugin config: %w", err)
					}
				case *pb.UpdateConfig_SocketConfig:
					if err := c.handleSocketConfig(r.UpdateConfig.GetAction(), r.UpdateConfig.GetSocketConfig()); err != nil {
						c.logger.Error("Failed to handle socket config", zap.Error(err))
						return fmt.Errorf("failed to handle socket config: %w", err)
					}
				default:
					c.logger.Error("unknown config type", zap.Any("type", t))
				}
			case *pb.ControlStreamReponse_TunnelCertificateSignResponse:
				if _, ok := c.requests[r.TunnelCertificateSignResponse.RequestId]; ok {
					select {
					case c.requests[r.TunnelCertificateSignResponse.RequestId] <- msg.response:
					default:
						c.logger.Error("failed to send response to request channel", zap.String("request_id", r.TunnelCertificateSignResponse.RequestId))
					}
				} else {
					c.logger.Error("unknown request id", zap.String("request_id", r.TunnelCertificateSignResponse.RequestId))
				}
			case *pb.ControlStreamReponse_Heartbeat:
			default:
				c.logger.Error("unknown message type", zap.Any("type", r))
			}
		}
	}
}

func (c *ConnectorService) newConnectorClient(ctx context.Context) (*grpc.ClientConn, error) {
	var grpcOpts []grpc.DialOption

	if c.config.ConnectorInsecureTransport {
		grpcOpts = append(grpcOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		grpcOpts = append(grpcOpts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})))
	}

	grpcOpts = append(
		grpcOpts,
		grpc.WithPerRPCCredentials(&tokenAuth{token: c.config.Token, insecure: c.config.ConnectorInsecureTransport}),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                20 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)

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

	var knowPlugins []string

	for _, config := range pluginConfig {
		var action pb.Action
		if _, ok := c.plugins[config.GetName()]; ok {
			action = pb.Action_UPDATE
		} else {
			action = pb.Action_CREATE
		}

		if err := c.handlePluginConfig(action, config); err != nil {
			return fmt.Errorf("failed to handle plugin config: %w", err)
		}

		knowPlugins = append(knowPlugins, config.GetName())
	}

	for name := range c.plugins {
		var found bool
		for _, knowPlugin := range knowPlugins {
			if name == knowPlugin {
				found = true
				break
			}
		}

		if !found {
			if err := c.handlePluginConfig(pb.Action_DELETE, &pb.PluginConfig{Name: name}); err != nil {
				return fmt.Errorf("failed to handle plugin config: %w", err)
			}
		}
	}

	var knowSockets []string

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

		knowSockets = append(knowSockets, config.GetId())
	}

	for id := range c.sockets {
		var found bool
		for _, knowSocket := range knowSockets {
			if id == knowSocket {
				found = true
				break
			}
		}

		if !found {
			if err := c.handleSocketConfig(pb.Action_DELETE, &pb.SocketConfig{Id: id}); err != nil {
				return fmt.Errorf("failed to handle socket config: %w", err)
			}
		}
	}

	c.backoff.Reset()

	return nil
}

func (c *ConnectorService) handlePluginConfig(action pb.Action, config *pb.PluginConfig) error {
	switch action {
	case pb.Action_CREATE:
		c.logger.Info("adding plugin", zap.Any("plugin", config))

		if _, ok := c.plugins[config.GetName()]; ok {
			return fmt.Errorf("plugin already exists")
		}

		plugin, err := plugin.NewPlugin(c.context, c.logger, config)
		if err != nil {
			return fmt.Errorf("failed to register plugin: %w", err)
		}

		c.plugins[config.GetName()] = plugin
	case pb.Action_UPDATE:
		c.logger.Info("updating plugin", zap.Any("plugin", config))

		plugin, ok := c.plugins[config.GetName()]
		if !ok {
			return fmt.Errorf("plugin does not exist")
		}

		if err := plugin.Update(config); err != nil {
			return fmt.Errorf("failed to update plugin: %w", err)
		}

		return nil
	case pb.Action_DELETE:
		c.logger.Info("deleting plugin", zap.Any("plugin", config))

		plugin, ok := c.plugins[config.GetName()]
		if !ok {
			return fmt.Errorf("plugin does not exists")
		}

		if err := plugin.Delete(); err != nil {
			return fmt.Errorf("failed to delete plugin: %w", err)
		}

		delete(c.plugins, config.GetName())
		return nil

	default:
		return fmt.Errorf("unknown action: %s", action)
	}

	return nil
}

func (c *ConnectorService) handleSocketConfig(action pb.Action, config *pb.SocketConfig) error {
	switch action {
	case pb.Action_CREATE:
		c.logger.Info("adding socket", zap.Any("socket", config))

		if _, ok := c.sockets[config.GetId()]; ok {
			return fmt.Errorf("socket already exists")
		}

		socket, err := c.newSocket(config)
		if err != nil {
			return fmt.Errorf("failed to register plugin: %w", err)
		}

		c.sockets[config.GetId()] = socket

	case pb.Action_UPDATE:
		c.logger.Info("updating socket", zap.Any("socket", config))

		// plugin, ok := c.plugins[config.GetId()]
		// if !ok {
		// 	return fmt.Errorf("plugin does not exist")
		// }

		// if err := plugin.Update(config); err != nil {
		// 	return fmt.Errorf("failed to update plugin: %w", err)
		// }

		return nil
	case pb.Action_DELETE:
		c.logger.Info("deleting socket", zap.Any("socket", config))

		socket, ok := c.sockets[config.GetId()]
		if !ok {
			return fmt.Errorf("socket does not exists")
		}

		delete(c.sockets, config.GetId())

		if err := socket.Close(); err != nil {
			return fmt.Errorf("failed to close socket: %w", err)
		}
		return nil

	default:
		return fmt.Errorf("unknown action: %s", action)
	}

	return nil
}

func (c *ConnectorService) newSocket(config *pb.SocketConfig) (*border0.Socket, error) {

	s := models.Socket{
		SocketID:   config.GetId(),
		SocketType: config.GetType(),
		SSHServer:  config.GetSshServer(),
		// ConnectorAuthenticationEnabled: true,
	}

	socket, err := border0.NewSocketFromConnectorAPI(c.context, c, s, c.organization)
	if err != nil {
		return nil, fmt.Errorf("failed to create socket: %w", err)
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

	connectorID, ok := claims["connector_id"]
	if !ok {
		return "", fmt.Errorf("failed to parse token")
	}

	connectorIDStr, ok := connectorID.(string)
	if !ok {
		return "", fmt.Errorf("failed to parse token")
	}

	return strings.ReplaceAll(connectorIDStr, "-", ""), nil
}

func (c *ConnectorService) SignSSHKey(ctx context.Context, socketID string, publicKey []byte) (string, string, error) {
	if c.stream == nil {
		return "", "", fmt.Errorf("stream is not connected")
	}

	requestId := uuid.New().String()
	if err := c.stream.Send(&pb.ControlStreamRequest{
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
	c.requests[requestId] = recChan

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

		delete(c.requests, response.GetRequestId())
		return response.GetCertificate(), response.GetHostkey(), nil
	}
}

func (c *ConnectorService) Listen(socket *border0.Socket) {
	l, err := socket.Listen()
	if err != nil {
		c.logger.Error("failed to start listener", zap.String("socket", socket.SocketID), zap.Error(err))
		return
	}

	var handlerConfig *sqlauthproxy.Config
	if socket.SocketType == "database" {
		handlerConfig, err = sqlauthproxy.BuildHandlerConfig(*socket.Socket)
		if err != nil {
			c.logger.Error("failed to create config for socket", zap.String("socket", socket.SocketID), zap.Error(err))
		}
	}

	var sshProxyConfig *ssh.ProxyConfig
	if socket.SocketType == "ssh" {
		sshProxyConfig, err = ssh.BuildProxyConfig(*socket.Socket, socket.Socket.AWSRegion, "")
		if err != nil {
			c.logger.Error("failed to create config for socket", zap.String("socket", socket.SocketID), zap.Error(err))
		}
	}

	switch {
	case socket.Socket.SSHServer && socket.SocketType == "ssh":
		sshServer := ssh.NewServer(c.organization.Certificates["ssh_public_key"])
		if err := sshServer.Serve(l); err != nil {
			c.logger.Error("ssh server failed", zap.String("socket", socket.SocketID), zap.Error(err))
		}
	case sshProxyConfig != nil:
		if err := ssh.Proxy(l, *sshProxyConfig); err != nil {
			c.logger.Error("ssh proxy failed", zap.String("socket", socket.SocketID), zap.Error(err))
		}
	case handlerConfig != nil:
		if err := sqlauthproxy.Serve(l, *handlerConfig); err != nil {
			c.logger.Error("sql proxy failed", zap.String("socket", socket.SocketID), zap.Error(err))
		}
	default:
		// if err := border0.Serve(l, socket.ConnectorData.TargetHostname, socket.ConnectorData.Port); err != nil {
		if err := border0.Serve(l, "127.0.0.1", 2222); err != nil {
			c.logger.Error("proxy failed", zap.String("socket", socket.SocketID), zap.Error(err))
		}
	}
}
