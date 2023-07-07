package plugin

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/borderzero/border0-go/lib/types/pointer"
	"github.com/borderzero/border0-go/lib/types/slice"
	"github.com/borderzero/border0-go/service/connector/types"
	"github.com/borderzero/discovery"
	"github.com/borderzero/discovery/discoverers"
	"github.com/borderzero/discovery/engines"
	"go.uber.org/zap"
)

func newPlugin(id string, logger *zap.Logger, engine discovery.Engine) Plugin {
	return &pluginImpl{
		ID:     id,
		logger: logger,
		engine: engine,
	}
}

func newAwsEc2DiscoveryPlugin(
	ctx context.Context,
	logger *zap.Logger,
	pluginId string,
	config *types.AwsEc2DiscoveryPluginConfiguration,
) (Plugin, error) {
	if config == nil {
		return nil, fmt.Errorf("Received nil ec2 discovery plugin configuration for plugin %s", pluginId)
	}

	awsConfigs, err := getAwsConfigs(ctx, config.BaseAwsPluginConfiguration)
	if err != nil {
		return nil, fmt.Errorf("failed to build AWS configurations for plugin %s", pluginId)
	}

	engineOpts := []engines.ContinuousEngineOption{}
	for _, awsConfig := range awsConfigs {
		engineOpts = append(engineOpts, engines.WithDiscoverer(
			discoverers.NewAwsEc2Discoverer(
				awsConfig,
				discoverers.WithAwsEc2DiscovererDiscovererId(fmt.Sprintf("aws ec2 %s", awsConfig.Region)),
				discoverers.WithAwsEc2DiscovererIncludedInstanceStates(
					slice.Transform(
						config.IncludeWithStates,
						func(s string) ec2types.InstanceStateName { return ec2types.InstanceStateName(s) },
					)...,
				),
				discoverers.WithAwsEc2DiscovererInclusionInstanceTags(config.IncludeWithTags),
				discoverers.WithAwsEc2DiscovererExclusionInstanceTags(config.ExcludeWithTags),
			),
			engines.WithInitialInterval(time.Duration(config.ScanIntervalMinutes)*time.Minute),
		))
	}

	return newPlugin(pluginId, logger, engines.NewContinuousEngine(engineOpts...)), nil
}

func newAwsEcsDiscoveryPlugin(
	ctx context.Context,
	logger *zap.Logger,
	pluginId string,
	config *types.AwsEcsDiscoveryPluginConfiguration,
) (Plugin, error) {
	if config == nil {
		return nil, fmt.Errorf("Received nil ecs discovery plugin configuration for plugin %s", pluginId)
	}

	awsConfigs, err := getAwsConfigs(ctx, config.BaseAwsPluginConfiguration)
	if err != nil {
		return nil, fmt.Errorf("failed to build AWS configurations for plugin %s", pluginId)
	}

	engineOpts := []engines.ContinuousEngineOption{}
	for _, awsConfig := range awsConfigs {
		engineOpts = append(engineOpts, engines.WithDiscoverer(
			discoverers.NewAwsEcsDiscoverer(
				awsConfig,
				discoverers.WithAwsEcsDiscovererDiscovererId(fmt.Sprintf("aws ecs %s", awsConfig.Region)),
				discoverers.WithAwsEcsDiscovererIncludedClusterStatuses(config.IncludeWithStatuses...),
				discoverers.WithAwsEcsDiscovererInclusionClusterTags(config.IncludeWithTags),
				discoverers.WithAwsEcsDiscovererExclusionClusterTags(config.ExcludeWithTags),
			),
			engines.WithInitialInterval(time.Duration(config.ScanIntervalMinutes)*time.Minute),
		))
	}
	engine := engines.NewContinuousEngine(engineOpts...)

	return newPlugin(pluginId, logger, engine), nil
}

func newAwsRdsDiscoveryPlugin(
	ctx context.Context,
	logger *zap.Logger,
	pluginId string,
	config *types.AwsRdsDiscoveryPluginConfiguration,
) (Plugin, error) {
	if config == nil {
		return nil, fmt.Errorf("received nil rds discovery plugin configuration for plugin %s", pluginId)
	}

	awsConfigs, err := getAwsConfigs(ctx, config.BaseAwsPluginConfiguration)
	if err != nil {
		return nil, fmt.Errorf("failed to build AWS configurations for plugin %s", pluginId)
	}

	engineOpts := []engines.ContinuousEngineOption{}
	for _, awsConfig := range awsConfigs {
		engineOpts = append(engineOpts, engines.WithDiscoverer(
			discoverers.NewAwsRdsDiscoverer(
				awsConfig,
				discoverers.WithAwsRdsDiscovererDiscovererId(fmt.Sprintf("aws rds %s", awsConfig.Region)),
				discoverers.WithAwsRdsDiscovererIncludedInstanceStatuses(config.IncludeWithStatuses...),
				discoverers.WithAwsRdsDiscovererInclusionInstanceTags(config.IncludeWithTags),
				discoverers.WithAwsRdsDiscovererExclusionInstanceTags(config.ExcludeWithTags),
			),
			engines.WithInitialInterval(time.Duration(config.ScanIntervalMinutes)*time.Minute),
		))
	}
	engine := engines.NewContinuousEngine(engineOpts...)

	return newPlugin(pluginId, logger, engine), nil
}

// returns slice of aws configurations based on an aws-based plugin's configuration
func getAwsConfigs(ctx context.Context, awsPluginConfig types.BaseAwsPluginConfiguration) ([]aws.Config, error) {
	optFns := []func(*config.LoadOptions) error{}

	awsCredentials := awsPluginConfig.AwsCredentials
	if awsCredentials != nil {
		awsProfile := awsCredentials.AwsProfile
		if awsProfile != nil {
			optFns = append(optFns, config.WithSharedConfigProfile(*awsProfile))
		}
		awsAccessKeyId := pointer.ValueOrZero(awsCredentials.AwsAccessKeyId)
		awsSecretAccessKey := pointer.ValueOrZero(awsCredentials.AwsSecretAccessKey)
		awsSessionToken := pointer.ValueOrZero(awsCredentials.AwsSessionToken)
		if awsAccessKeyId != "" && awsSecretAccessKey != "" {
			optFns = append(optFns, config.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider(
					awsAccessKeyId,
					awsSecretAccessKey,
					awsSessionToken,
				),
			))
		}
	}

	// if no regions are provided we try to load without a region
	// and hopes that the current AWS profile has a region assigned.
	if len(awsPluginConfig.AwsRegions) == 0 {
		baseAwsConfig, err := config.LoadDefaultConfig(ctx, optFns...)
		if err != nil {
			return nil, fmt.Errorf("failed to load base aws config: %v", err)
		}
		return []aws.Config{baseAwsConfig}, nil
	}

	// if regions are provided, we first build a base aws configuration which
	// we will use as the basis for all other configs (we will just update the
	// region). The reason why us-east-1 is provided below is that if the default
	// credential providers chain does not find a region, LoadDefaultConfig will
	// fail. There's nothing special about us-east-1 -- we could pick any here.
	baseAwsConfig, err := config.LoadDefaultConfig(ctx, append(optFns, config.WithRegion("us-east-1"))...)
	if err != nil {
		return nil, fmt.Errorf("failed to load base aws config: %v", err)
	}

	awsConfigs := []aws.Config{}
	for _, region := range awsPluginConfig.AwsRegions {
		baseAwsConfig.Region = region
		awsConfigs = append(awsConfigs, baseAwsConfig)
	}
	return awsConfigs, nil
}
