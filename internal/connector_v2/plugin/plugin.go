package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/borderzero/border0-go/service/connector/types"
	pb "github.com/borderzero/border0-proto/connector"
	"github.com/borderzero/discovery"
	"github.com/borderzero/discovery/discoverers"
	"github.com/borderzero/discovery/engines"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/structpb"
)

func NewPlugin(ctx context.Context, logger *zap.Logger, plugin *pb.PluginConfig) (Plugin, error) {
	var config types.PluginConfiguration
	if err := structpbToStruct(plugin.Config, &config); err != nil {
		return nil, err
	}

	switch strings.ToLower(plugin.Type) {
	case types.PluginTypeAwsEcsDiscovery:
		awsRegions := config.AwsEcsDiscoveryPluginConfiguration.AwsRegions
		if len(config.AwsEcsDiscoveryPluginConfiguration.AwsRegions) == 0 {
			fmt.Println("no regions specified, using default region")
			awsRegions = []string{""}
		}

		var discovers []engines.ContinuousEngineOption

		for _, region := range awsRegions {
			awsConfig, err := awsConfig(ctx, config.AwsEcsDiscoveryPluginConfiguration.BaseAwsPluginConfiguration, region)
			if err != nil {
				return nil, err
			}

			engine := engines.WithDiscoverer(
				discoverers.NewAwsEcsDiscoverer(awsConfig),
				engines.WithInitialInterval(time.Duration(config.AwsEcsDiscoveryPluginConfiguration.ScanIntervalSeconds)*time.Second),
			)

			discovers = append(discovers, engine)
		}

		if len(discovers) == 0 {
			return nil, fmt.Errorf("no engines created")
		}

		engine := engines.NewContinuousEngine(discovers...)
		plugin := &pluginImpl{
			ID:     plugin.Id,
			Name:   plugin.Name,
			engine: engine,
			logger: logger,
		}

		return plugin, nil
	case types.PluginTypeAwsEc2Discovery:
		awsRegions := config.AwsEc2DiscoveryPluginConfiguration.AwsRegions
		if len(config.AwsEc2DiscoveryPluginConfiguration.AwsRegions) == 0 {
			fmt.Println("no regions specified, using default region")
			awsRegions = []string{""}
		}

		var discovers []engines.ContinuousEngineOption

		for _, region := range awsRegions {
			awsConfig, err := awsConfig(ctx, config.AwsEc2DiscoveryPluginConfiguration.BaseAwsPluginConfiguration, region)
			if err != nil {
				return nil, err
			}

			engine := engines.WithDiscoverer(
				discoverers.NewAwsEc2Discoverer(awsConfig),
				engines.WithInitialInterval(time.Duration(config.AwsEc2DiscoveryPluginConfiguration.ScanIntervalSeconds)*time.Second),
			)

			discovers = append(discovers, engine)
		}

		if len(discovers) == 0 {
			return nil, fmt.Errorf("no engines created")
		}

		engine := engines.NewContinuousEngine(discovers...)
		plugin := &pluginImpl{
			ID:     plugin.Id,
			Name:   plugin.Name,
			engine: engine,
			logger: logger,
		}

		return plugin, nil
	case types.PluginTypeAwsRdsDiscovery:
		awsRegions := config.AwsRdsDiscoveryPluginConfiguration.AwsRegions
		if len(config.AwsRdsDiscoveryPluginConfiguration.AwsRegions) == 0 {
			fmt.Println("no regions specified, using default region")
			awsRegions = []string{""}
		}

		var discovers []engines.ContinuousEngineOption

		for _, region := range awsRegions {
			awsConfig, err := awsConfig(ctx, config.AwsRdsDiscoveryPluginConfiguration.BaseAwsPluginConfiguration, region)
			if err != nil {
				return nil, err
			}

			engine := engines.WithDiscoverer(
				discoverers.NewAwsRdsDiscoverer(awsConfig),
				engines.WithInitialInterval(time.Duration(config.AwsRdsDiscoveryPluginConfiguration.ScanIntervalSeconds)*time.Second),
			)

			discovers = append(discovers, engine)
		}

		if len(discovers) == 0 {
			return nil, fmt.Errorf("no engines created")
		}

		engine := engines.NewContinuousEngine(discovers...)
		plugin := &pluginImpl{
			ID:     plugin.Id,
			Name:   plugin.Name,
			engine: engine,
			logger: logger,
		}

		return plugin, nil
	default:
		return nil, fmt.Errorf("unknown plugin type: %s", plugin.Type)
	}
}

func awsConfig(ctx context.Context, pluginConfig types.BaseAwsPluginConfiguration, region string) (aws.Config, error) {
	var optFns []func(*config.LoadOptions) error

	if region != "" {
		optFns = append(optFns, config.WithRegion(region))
	}

	if pluginConfig.AwsCredentials != nil {
		if pluginConfig.AwsCredentials.AwsProfile != nil {
			optFns = append(optFns, config.WithSharedConfigProfile(*pluginConfig.AwsCredentials.AwsProfile))
		}

		if pluginConfig.AwsCredentials.AwsAccessKeyId != nil && pluginConfig.AwsCredentials.AwsSecretAccessKey != nil {
			var sessionToken string
			if pluginConfig.AwsCredentials.AwsSessionToken != nil {
				sessionToken = *pluginConfig.AwsCredentials.AwsSessionToken
			}

			optFns = append(optFns, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(*pluginConfig.AwsCredentials.AwsAccessKeyId, *pluginConfig.AwsCredentials.AwsSecretAccessKey, sessionToken)))
		}
	}

	awsConfig, err := config.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		return awsConfig, fmt.Errorf("failed to load aws config: %w", err)
	}

	return awsConfig, nil
}

func structpbToStruct(structpb *structpb.Struct, target any) error {
	jsonBytes, err := structpb.MarshalJSON()
	if err != nil {
		return fmt.Errorf("failed to unmarshal structpb: %w", err)
	}

	err = json.Unmarshal(jsonBytes, target)
	if err != nil {
		return fmt.Errorf("failed to unmarshal json: %v", err)
	}

	return nil
}

func (p *pluginImpl) Stop() error {
	p.cancel()

	return nil
}

func (p *pluginImpl) Start(ctx context.Context, csResults chan *PluginDiscoveryResults) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	p.cancel = cancel

	results := make(chan *discovery.Result)
	go p.engine.Run(ctx, results)

	for {
		select {
		case <-ctx.Done():
			return nil
		case result := <-results:
			p.logger.Debug("discovery result", zap.String("plugin", p.Name), zap.Int("#resources", len(result.Resources)))
			if len(result.Errors) > 0 {
				p.logger.Warn("discovery error", zap.String("plugin", p.Name), zap.Any("errors", result.Errors))
			}

			csResults <- &PluginDiscoveryResults{
				PluginID: p.ID,
				Result:   result,
			}
		}

	}
}
