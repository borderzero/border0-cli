package discover

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/connector/config"
)

type Ec2Discover struct {
	ec2API ec2iface.EC2API
}

type Ec2SocketData struct {
	Port  string `mapstructure:"port"`
	Type  string
	Group string
	Host  string
}

var _ Discover = (*Ec2Discover)(nil)

func NewEC2Discover(ec2API ec2iface.EC2API, cfg config.Config) *Ec2Discover {
	return &Ec2Discover{ec2API: ec2API}
}

func (s *Ec2Discover) SkipRun(ctx context.Context, cfg config.Config, state DiscoverState) bool {
	return false
}

func (s *Ec2Discover) Find(ctx context.Context, cfg config.Config, state DiscoverState) ([]models.Socket, error) {
	// find all instance running in the configured region
	params := &ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{
			{
				Name: aws.String("instance-state-name"),
				Values: []*string{
					aws.String("running"),
				},
			},
		},
	}

	res, err := s.ec2API.DescribeInstances(params)

	if err != nil {
		return nil, err
	}

	var sockets []models.Socket

	for _, group := range cfg.AwsGroups {
		for _, i := range res.Reservations {
			for _, ti := range i.Instances {
				var instanceName string

				// check the instance name in the tags
				for _, t := range ti.Tags {
					if *t.Key == "Name" {
						instanceName = *t.Value
					}
				}

				for _, t := range ti.Tags {
					if strings.HasPrefix(*t.Key, "border0") {
						socketData := parseLabels(*t.Value)

						if socketData.Group == group.Group {
							socket := s.buildSocket(cfg.Connector.Name, group, socketData, *ti, instanceName)
							sockets = append(sockets, *socket)
						}
					}
				}
			}
		}
	}

	return sockets, nil
}

func (s *Ec2Discover) buildSocket(connectorName string, group config.ConnectorGroups, socketData SocketDataTag, instance ec2.Instance, instanceName string) *models.Socket {
	socket := models.Socket{}
	socket.TargetPort, _ = strconv.Atoi(socketData.Port)
	socket.PolicyGroup = group.Group
	socket.InstanceId = *instance.InstanceId

	socket.SocketType = socketData.Type
	socket.AllowedEmailAddresses = group.AllowedEmailAddresses
	socket.AllowedEmailDomains = group.AllowedEmailDomains
	socket.PrivateSocket = group.PrivateSocket

	socket.UpstreamType = socketData.UpstreamType
	socket.UpstreamUsername = socketData.UpstreamUsername
	socket.UpstreamPassword = socketData.UpstreamPassword

	socket.TargetHostname = socketData.Host
	if socket.TargetHostname == "" || socket.TargetHostname == "<nil>" {
		socket.TargetHostname = *instance.PrivateIpAddress
	}

	socket.PolicyNames = group.Policies
	socket.CloudAuthEnabled = true

	socket.Name = buildSocketName(instanceName, connectorName, socket.SocketType, socketData.Name, true)
	if socket.PrivateSocket {
		socket.Dnsname = socket.Name
	}
	return &socket
}

func (s *Ec2Discover) Name() string {
	return reflect.TypeOf(s).Elem().Name()
}

func buildSocketName(instanceName, connectorName, socketType, supplyLabelName string, SuffixConnectorName bool) string {
	var tempName string
	var constructedName string
	if supplyLabelName != "" {
		tempName = supplyLabelName
	} else {
		tempName = instanceName
	}

	tempName = strings.Replace(tempName, "_", "-", -1)
	tempName = strings.Replace(tempName, ".", "-", -1)
	tempName = strings.Replace(tempName, " ", "-", -1)

	if socketType == "" {
		// In case Type is empty
		// Ideally we do the guessing before this, dont want to duplicate code. for now just ignore.
		if SuffixConnectorName {
			constructedName = fmt.Sprintf("%v-%v", tempName, connectorName)
		} else {
			constructedName = fmt.Sprintf("%v", tempName)
		}
	} else {
		if SuffixConnectorName {
			constructedName = fmt.Sprintf("%v-%v-%v", socketType, tempName, connectorName)
		} else {
			constructedName = fmt.Sprintf("%v-%v", socketType, tempName)
		}
	}
	return constructedName
}

func (s *Ec2Discover) WaitSeconds() int64 {
	return 10
}
