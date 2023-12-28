package sqlclientproxy

import (
	"crypto/tls"
	"fmt"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/client"
	"go.uber.org/zap"
)

type SqlClientProxy interface {
	Listen() error
}

type sqlClientProxy struct {
	port      int
	info      client.ResourceInfo
	resource  models.ClientResource
	tlsConfig *tls.Config
}

func NewSqlClientProxy(logger *zap.Logger, port int, resource models.ClientResource) (SqlClientProxy, error) {
	switch resource.DatabaseType {
	case "mysql":
		return newMysqlClientProxy(logger, port, resource)
	case "postgres":
		return newPostgresClientProxy(logger, port, resource)
	case "mssql":
		return newTcpProxy(logger, port, resource)
	default:
		return nil, fmt.Errorf("unsupported database type: %s", resource.DatabaseType)
	}
}
