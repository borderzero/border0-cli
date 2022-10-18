package discover

import (
	"context"
	"log"
	"net"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/connector/config"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"go.uber.org/zap"
	"k8s.io/utils/strings/slices"
)

type DockerFinder struct {
	Logger *zap.Logger
}

var _ Discover = (*DockerFinder)(nil)

func (s *DockerFinder) SkipRun(ctx context.Context, cfg config.Config, state DiscoverState) bool {
	return false
}

func (s *DockerFinder) Find(ctx context.Context, cfg config.Config, state DiscoverState) ([]models.Socket, error) {
	s.Logger.Info("Discovering docker containers")

	sockets := []models.Socket{}
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Println("Error creating docker client:", err)
		return nil, err
	}

	containers, err := cli.ContainerList(ctx, types.ContainerListOptions{})
	s.Logger.Debug("found containers", zap.Any("containers", containers))

	if err != nil {
		log.Println("Error getting containers:", err)
		return nil, err
	}

	// Let's determine if the connector runs in a docker container, and if so, what network id.
	connectorNetworkId, connectorGwIp, err := s.findNetworkID(containers)
	if err != nil {
		println("Error while trying to determine Network ID: ", err)
	}

	s.Logger.Debug("container info", zap.String("networkId", connectorNetworkId), zap.String("gwIp", connectorGwIp))

	for _, group := range cfg.DockerPlugin {
		s.Logger.Debug("discover group", zap.Any("group", group))

		for _, container := range containers {
			s.Logger.Debug("checking container", zap.String("container", container.ID), zap.Int("numer of labels", len(container.Labels)))

			labels := container.Labels
			var instanceName string
			if len(labels) > 0 {
				instanceName = container.Names[0]
				instanceName = strings.Replace(instanceName, "/", "", -1)
			}

			for k, v := range labels {
				s.Logger.Debug("checking label for container", zap.String("containerID", container.ID), zap.String("label", k), zap.String("value", v))

				if k == "Name" && instanceName == "" {
					instanceName = v
					s.Logger.Debug("found instance name", zap.String("containerID", container.ID), zap.String("instanceName", instanceName))

				}
				if strings.HasPrefix(strings.ToLower(k), "border0") {
					s.Logger.Debug("found border0 label", zap.String("containerID", container.ID), zap.String("label", k))

					metadata := parseLabels(v)
					if metadata.Group != "" && group.Group == metadata.Group {
						s.Logger.Debug("matching group", zap.String("containerID", container.ID), zap.String("group", group.Group))

						ip := s.extractIPAddress(container.NetworkSettings.Networks, connectorNetworkId, connectorGwIp)

						// Now determine the port
						// First check if it is defined in the labels, otherwise we'll take it from Docker ports
						metadataPort := 0
						metadataPort, _ = strconv.Atoi(metadata.Port)
						port := uint16(metadataPort)

						// Check what port we should return.
						// We default to the Private port.
						// But If we detect we run between networks, we should overwrite it to use the exposed port

						if connectorGwIp == ip {
							// This means, connector runs in a container, and is in a different namespace
							// So we assume no routing between networks, lets use
							port = s.extractPort(container.Ports, "public")
						}

						if port == 0 {
							// Not in label, so let's guess from the docker port
							port = s.extractPort(container.Ports, "private")
						}

						if port == 0 {
							s.Logger.Error("Could not determine container Port... ignoring instance: ", zap.String("instanceName", instanceName))
							continue
						}
						if ip == "" {
							s.Logger.Error("Could not determine container IP... ignoring instance: ", zap.String("instanceName", instanceName))
							continue
						}
						connector := cfg.Connector

						s.Logger.Info("add instance as socket", zap.String("instanceName", instanceName))
						sockets = append(sockets, s.buildSocket(cfg.Connector.Name, group, connector, metadata, container, instanceName, ip, port))
					} else {
						s.Logger.Debug("group not mached", zap.String("containerID", container.ID), zap.String("group", group.Group))
					}
				}
			}
		}
	}

	return sockets, nil
}

func (s *DockerFinder) buildSocket(connectorName string, group config.ConnectorGroups, connector config.Connector, socketData SocketDataTag, instance types.Container, instanceName, ipAddress string, port uint16) models.Socket {
	socket := models.Socket{}
	socket.TargetPort = int(port)
	socket.PolicyGroup = group.Group
	socket.InstanceId = instance.ID
	socket.PolicyNames = group.Policies
	socket.SocketType = socketData.Type
	socket.AllowedEmailAddresses = group.AllowedEmailAddresses
	socket.AllowedEmailDomains = group.AllowedEmailDomains

	socket.UpstreamType = socketData.UpstreamType
	socket.UpstreamUsername = socketData.UpstreamUsername
	socket.UpstreamPassword = socketData.UpstreamPassword

	socket.PrivateSocket = group.PrivateSocket

	socket.TargetHostname = socketData.Host
	if socket.TargetHostname == "" || socket.TargetHostname == "<nil>" {
		socket.TargetHostname = ipAddress
	}

	socket.Name = buildSocketName(instanceName, connectorName, socket.SocketType, socketData.Name, connector.SuffixConnectorName)
	if socket.PrivateSocket {
		socket.Dnsname = socket.Name
	}
	socket.CloudAuthEnabled = true
	return socket
}

func (s *DockerFinder) findNetworkID(containers []types.Container) (string, string, error) {
	ifas, err := net.Interfaces()
	if err != nil {
		return "", "", err
	}
	var macAddresses []string
	for _, ifa := range ifas {
		a := ifa.HardwareAddr.String()
		if a != "" {
			macAddresses = append(macAddresses, a)
		}
	}

	// Now we have a list of mac addresses.
	// Let's see if there are any container namespaces with that mac
	for _, container := range containers {
		for _, value := range container.NetworkSettings.Networks {
			if value.MacAddress != "" {
				if slices.Contains(macAddresses, value.MacAddress) {
					return value.NetworkID, value.Gateway, nil
				}
			}
		}
	}
	return "", "", nil

}
func (s *DockerFinder) extractIPAddress(networkSettings map[string]*network.EndpointSettings, connectorNetworkId string, connectorGwIp string) string {

	if connectorNetworkId != "" {
		// This means the connector likely run in a container.
		for _, value := range networkSettings {
			if value.NetworkID == connectorNetworkId {
				// This means we're in the same network.
				// So we can retunr the private IP

				if value.IPAddress != "" {
					return value.IPAddress
				}
			}
		}

		// If we get here, then we didnt run the same network.. so we should return the default GW IP of the connector
		for _, value := range networkSettings {
			// This means we're in the same network.
			// So we can retunr the private IP
			if value.IPAddress != "" {
				return connectorGwIp
			}
		}
	}
	// Otherwise fall through, this means we likely run on the host and not in a contaoiner
	// and just return the private IP, Could probably also be 127.0.0.1
	for _, value := range networkSettings {
		if value.IPAddress != "" {
			return value.IPAddress
		}
	}

	return ""
}

func (s *DockerFinder) extractPort(ports []types.Port, portType string) uint16 {
	// First try to find a port that is linked to an IP
	// Sometimes this field is empty, which is odd.
	re, _ := regexp.Compile(`^(([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])\.){3}([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])$`)

	if portType == "public" {
		for _, p := range ports {
			if p.Type == "tcp" && re.MatchString(p.IP) && p.PublicPort > 0 {
				return p.PublicPort
			}
		}
	} else {
		for _, p := range ports {
			if p.Type == "tcp" && re.MatchString(p.IP) && p.PrivatePort > 0 {
				return p.PrivatePort
			}
		}
	}
	// fall through
	// Otherwise return the first private port, even if IP is empty
	for _, p := range ports {
		if p.Type == "tcp" && p.PrivatePort > 0 {
			return p.PrivatePort
		}
	}

	return 0
}

func (s *DockerFinder) Name() string {
	return reflect.TypeOf(s).Elem().Name()
}

func (s *DockerFinder) WaitSeconds() int64 {
	return 10
}
