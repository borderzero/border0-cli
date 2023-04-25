package connector

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/borderzero/border0-cli/internal/api"
	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/connector/config"
	"github.com/borderzero/border0-cli/internal/connector/core"
	"github.com/borderzero/border0-cli/internal/connector/discover"
	"github.com/borderzero/border0-cli/internal/http"
	"github.com/golang-jwt/jwt"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type ConnectorService struct {
	cfg     config.Config
	logger  *zap.Logger
	version string
}

func NewConnectorService(cfg config.Config, logger *zap.Logger, version string) *ConnectorService {
	return &ConnectorService{cfg, logger, version}
}

func (c *ConnectorService) Start() error {
	log.Println("starting the connector service")

	ctx := context.Background()
	border0API := api.NewAPI()

	creds, err := c.fetchAccessToken(border0API)
	if err != nil {
		return err
	}

	//login with accesstoken or username and password
	border0API.With(api.WithCredentials(creds))
	//setup the version for border0
	border0API.With(api.WithVersion(c.version))

	var plugins []discover.Discover
	if len(c.cfg.AwsGroups) > 0 {
		sess, err := session.NewSessionWithOptions(session.Options{
			SharedConfigState: session.SharedConfigEnable,
			Profile:           c.cfg.Connector.AwsProfile,
			Config: aws.Config{
				Region: &c.cfg.Connector.AwsRegion,
			},
		})

		if err != nil {
			c.logger.Error("error creating the aws session", zap.Error(err))
		}

		if sess != nil {
			ec2Discover := discover.NewEC2Discover(ec2.New(sess), c.cfg)
			plugins = append(plugins, ec2Discover)
		}
	}

	if len(c.cfg.EcsPlugin) > 0 {
		ecsDiscover, err := discover.NewECSDiscover(c.cfg)
		if err != nil {
			c.logger.Error("error creating the ecs discover", zap.Error(err))
		}
		plugins = append(plugins, ecsDiscover)
	}

	if len(c.cfg.DockerPlugin) > 0 {
		plugins = append(plugins, &discover.DockerFinder{Logger: c.logger})
	}

	if len(c.cfg.NetworkPlugin) > 0 {
		plugins = append(plugins, &discover.NetworkFinder{})
	}

	if c.cfg.K8Plugin != nil {
		k8Discover := discover.NewK8Discover()
		if k8Discover != nil {
			plugins = append(plugins, k8Discover)
		}
	}

	// always load the static socket plugin
	plugins = append(plugins, &discover.StaticSocketFinder{})

	c.StartWithPlugins(ctx, c.cfg, border0API, plugins, c.buildMetadata(creds.AccessToken))

	return nil
}

func (c *ConnectorService) buildMetadata(accessToken string) core.Metadata {
	meta := core.Metadata{}

	token, _, err := new(jwt.Parser).ParseUnverified(accessToken, jwt.MapClaims{})
	if err != nil {
		c.logger.Warn("could not parse access token in order to build connector metadata")
		return meta
	}

	if token == nil || token.Claims == nil {
		c.logger.Warn("nil access token or claims, could not build connector metadata")
		return meta
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		c.logger.Warn("token claims could not be decoded to map")
		return meta
	}

	typeClaim, ok := claims["type"].(string)
	if !ok {
		c.logger.Warn("token claims did not contain \"type\"")
		return meta
	}
	tokenID, ok := claims["user_id"].(string)
	if !ok {
		c.logger.Warn("token claims did not contain \"user_id\"")
		return meta
	}

	meta.Principal = fmt.Sprintf("%s:%s", typeClaim, tokenID)

	return meta
}

func (c *ConnectorService) fetchAccessToken(border0API api.API) (*models.Credentials, error) {
	if c.cfg.Credentials.Token != "" {
		c.logger.Info("using token defined in config file")
		accessToken := c.cfg.Credentials.Token

		return models.NewCredentials(accessToken, models.CredentialsTypeToken), nil
	} else if c.cfg.Credentials.GetUsername() != "" && c.cfg.Credentials.Password != "" {
		c.logger.Info("logging in with username and password")

		resp, err := border0API.Login(c.cfg.Credentials.GetUsername(), c.cfg.Credentials.Password)
		if err != nil {
			return nil, fmt.Errorf("failed to login: %v", err)
		}

		return models.NewCredentials(resp.Token, models.CredentialsTypeUser), nil
	} else {
		c.logger.Info("using token defined in border0 file")
		accessToken, err := http.GetToken()
		if err != nil {
			return nil, err
		}

		return models.NewCredentials(accessToken, models.CredentialsTypeUser), nil
	}
}

func (c *ConnectorService) StartWithPlugins(ctx context.Context, cfg config.Config, border0API api.API, plugins []discover.Discover, metadata core.Metadata) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	g, groupCtx := errgroup.WithContext(ctx)

	for _, discoverPlugin := range plugins {
		connectorCore := core.NewConnectorCore(c.logger, c.cfg, discoverPlugin, border0API, metadata, c.version)

		socketUpdateCh := make(chan []models.Socket, 1)

		c.StartSocketWorker(groupCtx, connectorCore, socketUpdateCh, g)
		c.StartDiscovery(groupCtx, connectorCore, socketUpdateCh, g)
		connectorCore.TunnelConnectJob(groupCtx, g)
	}

	if err := g.Wait(); err != nil {
		c.logger.Info("Program terminated", zap.Error(err))
	}

	return nil
}

func (c ConnectorService) Stop() error {
	log.Println("stopping the connector service")
	return nil
}

func (c *ConnectorService) StartSocketWorker(ctx context.Context, connectorCore *core.ConnectorCore, socketUpdateCh chan []models.Socket, group *errgroup.Group) {
	group.Go(func() error {
		for {
			select {
			case sockets := <-socketUpdateCh:
				c.logger.Info("receiving an update")
				connectorCore.HandleUpdates(ctx, sockets)
			case <-ctx.Done():
				return errors.New("context canceled")
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
}

func (c *ConnectorService) StartDiscovery(ctx context.Context, connectorCore *core.ConnectorCore, socketUpdateCh chan []models.Socket, group *errgroup.Group) {
	group.Go(func() error {
		for {
			select {
			case <-ctx.Done():
				return errors.New("context canceled")
			default:
				connectorCore.DiscoverNewSocketChanges(ctx, socketUpdateCh)
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
}
