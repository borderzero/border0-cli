package upstreamdata

import (
	"fmt"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-go/types/service"
)

func (u *UpstreamDataBuilder) buildUpstreamDataForHttpService(s *models.Socket, config *service.HttpServiceConfiguration) error {
	if config == nil {
		return fmt.Errorf("got http service with no http service configuration")
	}

	if config.HttpServiceType == service.HttpServiceTypeStandard {
		hostname := config.StandardHttpServiceConfiguration.Hostname
		port := config.StandardHttpServiceConfiguration.Port
		hostSniHeader := config.StandardHttpServiceConfiguration.HostSniHeader

		hostname = u.fetchVariableFromSource(hostname)

		s.ConnectorData.TargetHostname = hostname
		s.ConnectorData.Port = int(port)
		s.TargetHostname = hostname
		s.TargetPort = int(port)
		s.UpstreamHttpHostname = &hostSniHeader
	}

	return nil
}
