package upstreamdata

import (
	"context"
	"fmt"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/lib/varsource"
	"github.com/borderzero/border0-go/types/service"
	"go.uber.org/zap"
)

// TODO: delete these and use SDK types.
//
// We leave these here for now to avoid touching the logic
// that sets up the upstream connections (using these names).
const (
	UpstreamTypeSSH        = "ssh"
	UpstreamTypeAwsSSM     = "aws-ssm"
	UpstreamTypeAwsEC2Conn = "aws-ec2connect"
)

// UpstreamDataBuilder builds a connector socket's upstream
// data given upstream configuration from the border0 api.
type UpstreamDataBuilder struct {
	vs     varsource.VariableSource
	logger *zap.Logger
}

// NewUpstreamDataBuilder is the UpstreamDataBuilder constructor.
func NewUpstreamDataBuilder(logger *zap.Logger) *UpstreamDataBuilder {
	return &UpstreamDataBuilder{varsource.NewDefaultVariableSource(), logger}
}

// Build populates the socket with the given upstream data.
func (u *UpstreamDataBuilder) Build(s *models.Socket, config service.Configuration) error {
	switch s.SocketType {
	case service.ServiceTypeDatabase:
		return u.buildUpstreamDataForDatabaseService(s, config.DatabaseServiceConfiguration)
	case service.ServiceTypeHttp:
		return u.buildUpstreamDataForHttpService(s, config.HttpServiceConfiguration)
	case service.ServiceTypeSsh:
		return u.buildUpstreamDataForSshService(s, config.SshServiceConfiguration)
	// deprecated
	case service.ServiceTypeTls:
		return u.buildUpstreamDataForTlsService(s, config.TlsServiceConfiguration)
	default:
		return fmt.Errorf("unsupported service type: %s", s.SocketType)
	}
}

func (u *UpstreamDataBuilder) fetchVariableFromSource(field string) string {
	val, err := u.vs.GetVariable(context.Background(), field)
	if err != nil {
		u.logger.Info("error fetching variable from upstream source", zap.String("variable_definition", field), zap.Error(err))
		return field
	}
	return val
}
