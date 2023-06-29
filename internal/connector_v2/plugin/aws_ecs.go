package plugin

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/borderzero/border0-cli/internal/connector_v2/util"
	pb "github.com/borderzero/border0-proto/connector"
	"go.uber.org/zap"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

type AwsECSPlugin struct {
	context context.Context
	cancel  context.CancelFunc
	logger  *zap.Logger

	name     string
	id       string
	typeName string
	config   awsECSPluginConfig
}

type awsECSPluginConfig struct {
	DiscoveryInterval uint   `json:"discoveryInterval"`
	AwsProfile        string `json:"awsProfile"`
	AwsRegion         string `json:"awsRegion"`
}

func NewAwsEcsPlugin(ctx context.Context, l *zap.Logger, pluginConfig *pb.PluginConfig) (*AwsECSPlugin, error) {
	context, cancel := context.WithCancel(ctx)

	var config awsECSPluginConfig
	if err := util.AsStruct(pluginConfig.Config, &config); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to parse configuration: %w", err)
	}
	if err := config.validate(); err != nil {
		cancel()
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	plugin := &AwsECSPlugin{
		context: context,
		cancel:  cancel,
		logger:  l,

		name:     pluginConfig.Name,
		typeName: pluginConfig.Type,

		config: config,
	}

	plugin.start()
	return plugin, nil
}

func (c awsECSPluginConfig) validate() error {
	if c.DiscoveryInterval < 1 {
		return fmt.Errorf("discoveryInterval must be greater than 0")
	}
	return nil
}

func (p *AwsECSPlugin) Update(pluginConfig *pb.PluginConfig) error {
	p.name = pluginConfig.GetName()

	var config awsECSPluginConfig
	if err := util.AsStruct(pluginConfig.Config, &config); err != nil {
		return fmt.Errorf("failed to parse configuration: %w", err)
	}

	if err := config.validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	p.config = config

	return nil
}

func (p *AwsECSPlugin) Delete() error {
	p.cancel()

	return nil
}

func (p *AwsECSPlugin) start() {
	go func() {
		for {
			if err := p.discover(); err != nil {
				p.logger.Error("failed to discover aws ecs resources", zap.Error(err))
			}

			select {
			case <-p.context.Done():
				p.logger.Info("aws ecs plugin stopped")
				return
			case <-time.After(time.Duration(p.config.DiscoveryInterval) * time.Second):
				continue
			}
		}
	}()
}

func (p *AwsECSPlugin) discover() error {
	p.logger.Info("discovering aws ecs resources")

	ecsSvc, err := p.newEcsClient()
	if err != nil {
		return fmt.Errorf("failed to create ecs client: %s", err)
	}

	var clusters []string
	input := &ecs.ListClustersInput{}
	for {
		output, err := ecsSvc.ListClusters(context.TODO(), input)
		if err != nil {
			return fmt.Errorf("unable to list clusters, %v", err)
		}

		clusters = append(clusters, output.ClusterArns...)
		if output.NextToken == nil {
			break
		}
		input.NextToken = output.NextToken
	}

	p.logger.Info("discovered clusters", zap.Strings("clusters", clusters))

	return nil
}

func (p *AwsECSPlugin) newEcsClient() (*ecs.Client, error) {
	var awsConfig aws.Config
	var err error
	if p.config.AwsProfile != "" {
		awsConfig, err = awsconfig.LoadDefaultConfig(p.context)
	} else {
		awsConfig, err = awsconfig.LoadDefaultConfig(p.context,
			awsconfig.WithSharedConfigProfile(p.config.AwsProfile))
	}

	if err != nil {
		return nil, fmt.Errorf("failed to load aws config: %s", err)
	}

	if p.config.AwsRegion != "" {
		awsConfig.Region = p.config.AwsRegion
	}

	ecsSvc := ecs.NewFromConfig(awsConfig)

	return ecsSvc, nil
}
