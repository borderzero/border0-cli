package plugin

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
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
		return nil, fmt.Errorf("received nil ec2 discovery plugin configuration for plugin %s", pluginId)
	}

	awsConfigs, err := getAwsConfigs(ctx, config.BaseAwsPluginConfiguration)
	if err != nil {
		return nil, fmt.Errorf("failed to build AWS configurations for plugin %s: %v", pluginId, err)
	}

	engineOpts := []engines.ContinuousEngineOption{}
	for _, awsConfig := range awsConfigs {
		engineOpts = append(engineOpts, engines.WithDiscoverer(
			discoverers.NewAwsEc2Discoverer(
				awsConfig,
				discoverers.WithAwsEc2DiscovererSsmStatusCheck(config.CheckSsmStatus, false),
				discoverers.WithAwsEc2DiscovererDiscovererId(fmt.Sprintf("aws ec2 ( region = %s )", awsConfig.Region)),
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
		return nil, fmt.Errorf("received nil ecs discovery plugin configuration for plugin %s", pluginId)
	}

	awsConfigs, err := getAwsConfigs(ctx, config.BaseAwsPluginConfiguration)
	if err != nil {
		return nil, fmt.Errorf("failed to build AWS configurations for plugin %s: %v", pluginId, err)
	}

	engineOpts := []engines.ContinuousEngineOption{}
	for _, awsConfig := range awsConfigs {
		engineOpts = append(engineOpts, engines.WithDiscoverer(
			discoverers.NewAwsEcsDiscoverer(
				awsConfig,
				discoverers.WithAwsEcsDiscovererDiscovererId(fmt.Sprintf("aws ecs ( region = %s )", awsConfig.Region)),
				discoverers.WithAwsEcsDiscovererInclusionServiceTags(config.IncludeWithTags),
				discoverers.WithAwsEcsDiscovererExclusionServiceTags(config.ExcludeWithTags),
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
		return nil, fmt.Errorf("failed to build AWS configurations for plugin %s: %v", pluginId, err)
	}

	engineOpts := []engines.ContinuousEngineOption{}
	for _, awsConfig := range awsConfigs {
		engineOpts = append(engineOpts, engines.WithDiscoverer(
			discoverers.NewAwsRdsDiscoverer(
				awsConfig,
				discoverers.WithAwsRdsDiscovererDiscovererId(fmt.Sprintf("aws rds ( region = %s )", awsConfig.Region)),
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

func newKubernetesDiscoveryPlugin(ctx context.Context,
	logger *zap.Logger,
	pluginId string,
	config *types.KubernetesDiscoveryPluginConfiguration,
) (Plugin, error) {
	if config == nil {
		return nil, fmt.Errorf("received nil kubernetes discovery plugin configuration for plugin %s", pluginId)
	}

	baseDiscovererOpts := []discoverers.KubernetesDiscovererOption{
		discoverers.WithKubernetesDiscovererInclusionServiceLabels(config.IncludeWithLabels),
		discoverers.WithKubernetesDiscovererExclusionServiceLabels(config.ExcludeWithLabels),
	}
	if config.KubernetesCredentials != nil {
		if config.KubernetesCredentials.MasterUrl != nil {
			baseDiscovererOpts = append(
				baseDiscovererOpts,
				discoverers.WithKubernetesDiscovererMasterUrl(*config.KubernetesCredentials.MasterUrl),
			)
		}
		if config.KubernetesCredentials.KubeconfigPath != nil {
			path := *config.KubernetesCredentials.KubeconfigPath
			if strings.HasPrefix(path, "~") {
				path = fmt.Sprintf("%s%s", os.Getenv("HOME"), strings.TrimPrefix(path, "~"))
			}
			baseDiscovererOpts = append(
				baseDiscovererOpts,
				discoverers.WithKubernetesDiscovererKubeconfigPath(path),
			)
		}
	}

	namespaces := config.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{"default"}
	}

	engineOpts := []engines.ContinuousEngineOption{}
	for _, namespace := range namespaces {
		engineOpts = append(engineOpts, engines.WithDiscoverer(
			discoverers.NewKubernetesDiscoverer(append(
				baseDiscovererOpts,
				discoverers.WithKubernetesDiscovererNamespace(namespace),
				discoverers.WithKubernetesDiscovererDiscovererId(fmt.Sprintf("kubernetes ( namespace = %s )", namespace)),
			)...),
			engines.WithInitialInterval(time.Duration(config.ScanIntervalMinutes)*time.Minute),
		))
	}
	engine := engines.NewContinuousEngine(engineOpts...)

	return newPlugin(pluginId, logger, engine), nil
}

func newDockerDiscoveryPlugin(ctx context.Context,
	logger *zap.Logger,
	pluginId string,
	config *types.DockerDiscoveryPluginConfiguration,
) (Plugin, error) {
	if config == nil {
		return nil, fmt.Errorf("received nil docker discovery plugin configuration for plugin %s", pluginId)
	}

	engine := engines.NewContinuousEngine(
		engines.WithDiscoverer(
			discoverers.NewDockerDiscoverer(
				discoverers.WithDockerDiscovererInclusionContainerLabels(config.IncludeWithLabels),
				discoverers.WithDockerDiscovererExclusionContainerLabels(config.ExcludeWithLabels),
			),
			engines.WithInitialInterval(time.Duration(config.ScanIntervalMinutes)*time.Minute),
		),
	)

	return newPlugin(pluginId, logger, engine), nil
}

func newNetworkDiscoveryPlugin(ctx context.Context,
	logger *zap.Logger,
	pluginId string,
	config *types.NetworkDiscoveryPluginConfiguration,
) (Plugin, error) {
	if config == nil {
		return nil, fmt.Errorf("received nil network discovery plugin configuration for plugin %s", pluginId)
	}

	ds := []discovery.Discoverer{}
	for index, target := range config.Targets {
		ports := slice.Transform(target.Ports, func(i uint16) string { return fmt.Sprint(i) })

		ds = append(ds, discoverers.NewNetworkDiscoverer(
			discoverers.WithNetworkDiscovererDiscovererId(fmt.Sprintf("network [%d/%d] ( target = %s )", index+1, len(config.Targets), target.Target)),
			discoverers.WithNetworkDiscovererTargets(target.Target),
			discoverers.WithNetworkDiscovererPorts(ports...),
		))
	}

	engineOpts := []engines.ContinuousEngineOption{}
	for _, discoverer := range ds {
		engineOpts = append(engineOpts, engines.WithDiscoverer(
			discoverer,
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
	// and hopes that the current AWS profile has a region defined.
	if len(awsPluginConfig.AwsRegions) == 0 {
		baseAwsConfig, err := config.LoadDefaultConfig(ctx, optFns...)
		if err != nil {
			return nil, fmt.Errorf("failed to load base aws config: %v", err)
		}
		// if the loaded default configuration does not have a region
		// we will try to get the region from ec2 instance metadata.
		// this will only work when running within an ec2 instance.
		if baseAwsConfig.Region == "" {
			identityDoc, err := imds.NewFromConfig(baseAwsConfig).GetInstanceIdentityDocument(ctx, &imds.GetInstanceIdentityDocumentInput{})
			if err != nil {
				return nil, fmt.Errorf("aws configuration did not have a region defined and failed to deduce it")
			}
			baseAwsConfig.Region = identityDoc.Region
		}
		return []aws.Config{baseAwsConfig}, nil
	}

	// if regions are provided, we first build a base aws configuration which
	// we will use as the basis for all other configs (we will then just take
	// a copy of the base aws config and update the region for each of the aws
	// regions provided)
	baseAwsConfig, err := config.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		return nil, fmt.Errorf("failed to load base aws config: %v", err)
	}

	awsConfigs := []aws.Config{}
	for _, region := range awsPluginConfig.AwsRegions {
		newAwsConfig := baseAwsConfig.Copy()
		newAwsConfig.Region = region
		awsConfigs = append(awsConfigs, newAwsConfig)
	}
	return awsConfigs, nil
}
