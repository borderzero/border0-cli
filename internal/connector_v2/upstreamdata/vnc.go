package upstreamdata

import (
	"fmt"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-go/types/service"
)

func (u *UpstreamDataBuilder) buildUpstreamDataForVncService(s *models.Socket, config *service.VncServiceConfiguration) error {
	if config == nil {
		return fmt.Errorf("got vnc service with no vnc service configuration")
	}

	hostname, port := config.Hostname, int(config.Port)
	s.ConnectorData.TargetHostname = hostname
	s.ConnectorData.Port = port
	s.TargetHostname = hostname
	s.TargetPort = port

	if config.Password != "" {
		s.ConnectorLocalData.UpstreamPassword = u.fetchVariableFromSource(config.Password)
	}

	return nil
}
