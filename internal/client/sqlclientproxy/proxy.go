package sqlclientproxy

import (
	"crypto/tls"
	"fmt"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/client"
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

func NewSqlClientProxy(port int, resource models.ClientResource) (SqlClientProxy, error) {
	switch resource.DatabaseType {
	case "mysql":
		return newMysqlClientProxy(port, resource)
	case "postgres":
		return newPostgresClientProxy(port, resource)
	default:
		return nil, fmt.Errorf("unsupported database type: %s", resource.DatabaseType)
	}
}
