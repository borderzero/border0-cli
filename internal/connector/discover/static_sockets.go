package discover

import (
	"context"
	"log"
	"reflect"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/connector/config"
)

type StaticSocketFinder struct{}

var _ Discover = (*StaticSocketFinder)(nil)

func (s *StaticSocketFinder) SkipRun(ctx context.Context, cfg config.Config, state DiscoverState) bool {
	return false
}

func (s *StaticSocketFinder) WaitSeconds() int64 {
	return 30
}

func (s *StaticSocketFinder) Find(ctx context.Context, cfg config.Config, state DiscoverState) ([]models.Socket, error) {
	sockets := []models.Socket{}
	for _, socketMap := range cfg.Sockets {
		socket := models.Socket{}

		for k, v := range socketMap {
			socket.Name = k
			socket.AllowedEmailAddresses = v.AllowedEmailAddresses
			socket.AllowedEmailDomains = v.AllowedEmailDomains
			socket.SocketType = v.Type
			socket.TargetHostname = v.Host
			socket.TargetPort = v.Port
			socket.ConnectorAuthenticationEnabled = v.ConnectorAuthenticationEnabled
			socket.UpstreamHttpHostname = v.UpstreamHttpHostname
			socket.CloudAuthEnabled = true
			socket.PolicyNames = v.Policies
			socket.UpstreamType = v.UpstreamType

			if v.UpstreamUser == "" && v.UpstreamUserDeprecated != "" {
				log.Println("WARNING: upstream_user is deprecated, please use upstream_username instead")
				v.UpstreamUser = v.UpstreamUserDeprecated
			}

			socket.ConnectorLocalData = &models.ConnectorLocalData{
				UpstreamUsername:      v.UpstreamUser,
				UpstreamPassword:      v.UpstreamPassword,
				UpstreamCertFile:      v.UpstreamCertFile,
				UpstreamKeyFile:       v.UpstreamKeyFile,
				UpstreamCACertFile:    v.UpstreamCACertFile,
				UpstreamIdentifyFile:  v.UpstreamIdentifyFile,
				UpstreamTLS:           v.UpstreamTLS,
				RdsIAMAuth:            v.RdsIAMAuth,
				AWSRegion:             v.AWSRegion,
				AWSEC2Target:          v.AWSEC2Target,
				AWSEC2ConnectEnabled:  v.AWSEC2ConnectEnabled,
				AWSEC2AZ:              v.AWSEC2AZ,
				CloudSQLConnector:     v.CloudSQLConnector,
				CloudSQLIAMAuth:       v.CloudSQLIAMAuth,
				CloudSQLInstance:      v.CloudSQLInstance,
				GoogleCredentialsFile: v.GoogleCredentialsFile,
				SSHServer:             v.SSHServer,
				AWSECSCluster:         v.AWSECSCluster,
				AWSECSTasks:           v.TaskFilter,
				AWSECSContainers:      v.ContainerFilter,
				AWSECSServices:        v.ServiceFilter,
			}
		}

		sockets = append(sockets, socket)
	}

	return sockets, nil
}

func (s *StaticSocketFinder) Name() string {
	return reflect.TypeOf(s).Elem().Name()
}
