package discover

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	aws_config "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/connector/config"
)

type EcsDiscover struct {
	ecsSvc *ecs.Client
}

// type Ec2SocketData struct {
// 	Port  string `mapstructure:"port"`
// 	Type  string
// 	Group string
// 	Host  string
// }

var _ Discover = (*EcsDiscover)(nil)

func NewECSDiscover(cfg config.Config) (*EcsDiscover, error) {
	var awsConfig aws.Config
	var err error

	if cfg.Connector.AwsProfile == "" {
		awsConfig, err = aws_config.LoadDefaultConfig(context.TODO())
	} else {
		awsConfig, err = aws_config.LoadDefaultConfig(context.TODO(),
			aws_config.WithSharedConfigProfile(cfg.Connector.AwsProfile))
	}

	if err != nil {
		return nil, fmt.Errorf("failed to load aws config: %s", err)
	}

	if cfg.Connector.AwsRegion != "" {
		awsConfig.Region = cfg.Connector.AwsRegion
	}

	ecsSvc := ecs.NewFromConfig(awsConfig)

	return &EcsDiscover{ecsSvc: ecsSvc}, nil
}

func (s *EcsDiscover) SkipRun(ctx context.Context, cfg config.Config, state DiscoverState) bool {
	return false
}

func (s *EcsDiscover) Find(ctx context.Context, cfg config.Config, state DiscoverState) ([]models.Socket, error) {
	var clusters []string
	input := &ecs.ListClustersInput{}
	for {
		output, err := s.ecsSvc.ListClusters(context.TODO(), input)
		if err != nil {
			return nil, fmt.Errorf("unable to list clusters, %v", err)
		}

		clusters = append(clusters, output.ClusterArns...)
		if output.NextToken == nil {
			break
		}
		input.NextToken = output.NextToken
	}

	var sockets []models.Socket

	if len(clusters) == 0 {
		return sockets, nil
	}

	res, err := s.ecsSvc.DescribeClusters(context.TODO(), &ecs.DescribeClustersInput{Clusters: clusters, Include: []types.ClusterField{"TAGS"}})
	if err != nil {
		return nil, fmt.Errorf("unable to describe clusters, %v", err)
	}

	for _, plugin := range cfg.EcsPlugin {
		for _, cluster := range res.Clusters {
			for _, t := range cluster.Tags {
				if strings.HasPrefix(*t.Key, "border0") {
					socketData := parseLabelsWithDelimeter(*t.Value, "/")
					if socketData.Group == plugin.Group {
						socket := s.buildSocket(cfg.Connector, plugin, socketData, cluster)
						sockets = append(sockets, *socket)
					}
				}
			}
		}
	}

	return sockets, nil
}

func (s *EcsDiscover) buildSocket(connector config.Connector, plugin config.EcsPlugin, socketData SocketDataTag, cluster types.Cluster) *models.Socket {
	socket := models.Socket{}
	socket.TargetPort, _ = strconv.Atoi(socketData.Port)
	socket.PolicyGroup = plugin.Group
	socket.SocketType = "ssh"
	socket.UpstreamType = "aws-ssm"
	socket.ConnectorLocalData = &models.ConnectorLocalData{
		AWSECSCluster:    *cluster.ClusterName,
		AWSECSServices:   plugin.ServiceFilter,
		AWSECSTasks:      plugin.TaskFilter,
		AWSECSContainers: plugin.ContainerFilter,
	}
	socket.ConnectorAuthenticationEnabled = plugin.ConnectorAuthenticationEnabled
	socket.PolicyNames = plugin.Policies
	socket.CloudAuthEnabled = true

	socket.Name = s.buildSocketName(*cluster.ClusterName, connector.Name, socketData.Name)
	return &socket
}

func (s *EcsDiscover) Name() string {
	return reflect.TypeOf(s).Elem().Name()
}

func (s *EcsDiscover) buildSocketName(clusterName, connectorName, supplyLabelName string) string {
	var name string
	if supplyLabelName != "" {
		name = supplyLabelName
	} else {
		name = clusterName
	}

	name = strings.Replace(name, "_", "-", -1)
	name = strings.Replace(name, ".", "-", -1)
	name = strings.Replace(name, " ", "-", -1)

	name = fmt.Sprintf("%v-%v", name, connectorName)
	if len(name) > 63 {
		name = name[:63]
	}

	return name
}

func (s *EcsDiscover) WaitSeconds() int64 {
	return 60
}
