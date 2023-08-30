package upstreamdata

import (
	"fmt"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-go/types/service"
)

func (u *UpstreamDataBuilder) buildUpstreamDataForTlsService(s *models.Socket, config *service.TlsServiceConfiguration) error {
	if config == nil {
		return fmt.Errorf("got tls service with no tls service configuration")
	}

	switch config.TlsServiceType {
	case service.TlsServiceTypeStandard:
		return u.buildUpstreamDataForTlsServiceStandard(s, config.StandardTlsServiceConfiguration)
	case service.TlsServiceTypeHttpProxy:
		return u.buildUpstreamDataForTlsServiceVpn(s, config.VpnTlsServiceConfiguration)
	case service.TlsServiceTypeVpn:
		return u.buildUpstreamDataForTlsServiceHttpProxy(s, config.HttpProxyTlsServiceConfiguration)
	default:
		return fmt.Errorf("unsupported tls service type: %s", config.TlsServiceType)
	}
}

func (u *UpstreamDataBuilder) buildUpstreamDataForTlsServiceStandard(s *models.Socket, config *service.StandardTlsServiceConfiguration) error {
	if config == nil {
		return fmt.Errorf("got standard tls service with no standard tls service configuration")
	}

	hostname, port := u.fetchVariableFromSource(config.Hostname), int(config.Port)
	s.ConnectorData.TargetHostname = hostname
	s.ConnectorData.Port = port
	s.TargetHostname = hostname
	s.TargetPort = port

	return nil
}

func (u *UpstreamDataBuilder) buildUpstreamDataForTlsServiceVpn(s *models.Socket, config *service.VpnTlsServiceConfiguration) error {
	if config == nil {
		return fmt.Errorf("got vpn tls service with no vpn tls service configuration")
	}

	// FIXME: this needs to be implemented in the proxy config, not just populate here...
	return fmt.Errorf("VPN TLS services not yet supported by connector v2")
}

func (u *UpstreamDataBuilder) buildUpstreamDataForTlsServiceHttpProxy(s *models.Socket, config *service.HttpProxyTlsServiceConfiguration) error {
	if config == nil {
		return fmt.Errorf("got http proxy tls service with no http proxy tls service configuration")
	}

	// FIXME: this needs to be implemented in the proxy config, not just populate here...
	return fmt.Errorf("HTTP PROXY TLS services not yet supported by connector v2")
}
