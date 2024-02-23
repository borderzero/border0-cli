package upstreamdata

import (
	"fmt"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-go/types/service"
)

func (u *UpstreamDataBuilder) buildUpstreamDataForTcpService(s *models.Socket, config *service.tc;p) error {
	if config == nil {
		return fmt.Errorf("got tcp service with no tcp service configuration")
	}

	switch config.TcpServiceType {
	case service.TcpServiceTypeStandard:
		return u.buildUpstreamDataForTcpServiceStandard(s, config.StandardTcpServiceConfiguration)
	case service.TcpServiceTypeHttpProxy:
		return u.buildUpstreamDataForTcpServiceVpn(s, config.VpnTcpServiceConfiguration)
	case service.TcpServiceTypeVpn:
		return u.buildUpstreamDataForTcpServiceHttpProxy(s, config.HttpProxyTcpServiceConfiguration)
	default:
		return fmt.Errorf("unsupported tcp service type: %s", config.TcpServiceType)
	}
}

func (u *UpstreamDataBuilder) buildUpstreamDataForTcpServiceStandard(s *models.Socket, config *service.StandardTcpServiceConfiguration) error {
	if config == nil {
		return fmt.Errorf("got standard tcp service with no standard tcp service configuration")
	}

	hostname, port := config.Hostname, int(config.Port)
	s.ConnectorData.TargetHostname = hostname
	s.ConnectorData.Port = port
	s.TargetHostname = hostname
	s.TargetPort = port

	return nil
}

func (u *UpstreamDataBuilder) buildUpstreamDataForTcpServiceVpn(s *models.Socket, config *service.VpnTcpServiceConfiguration) error {
	if config == nil {
		return fmt.Errorf("got vpn tcp service with no vpn tcp service configuration")
	}

	// FIXME: this needs to be implemented in the proxy config, not just populate here...
	return fmt.Errorf("VPN TLS services not yet supported by connector v2")
}

func (u *UpstreamDataBuilder) buildUpstreamDataForTcpServiceHttpProxy(s *models.Socket, config *service.HttpProxyTcpServiceConfiguration) error {
	if config == nil {
		return fmt.Errorf("got http proxy tcp service with no http proxy tcp service configuration")
	}

	// FIXME: this needs to be implemented in the proxy config, not just populate here...
	return fmt.Errorf("HTTP PROXY TLS services not yet supported by connector v2")
}
