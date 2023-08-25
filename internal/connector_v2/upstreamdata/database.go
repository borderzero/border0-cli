package upstreamdata

import (
	"fmt"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-go/lib/types/pointer"
	"github.com/borderzero/border0-go/types/service"
)

func (u *UpstreamDataBuilder) buildUpstreamDataForDatabaseService(socket *models.Socket, config *service.DatabaseServiceConfiguration) error {
	if config == nil {
		return fmt.Errorf("got database service with no database service configuration")
	}

	switch config.DatabaseServiceType {
	case service.DatabaseServiceTypeStandard:
		return u.buildUpstreamDataForDatabaseServiceStandard(socket, config.Standard)
	case service.DatabaseServiceTypeAwsRds:
		return u.buildUpstreamDataForDatabaseServiceAwsRds(socket, config.AwsRds)
	case service.DatabaseServiceTypeGcpCloudSql:
		return u.buildUpstreamDataForDatabaseServiceGcpCloudSql(socket, config.GcpCloudSql)
	}

	return fmt.Errorf("unsupported database service type: %s", config.DatabaseServiceType)
}

func (u *UpstreamDataBuilder) buildUpstreamDataForDatabaseServiceStandard(socket *models.Socket, config *service.StandardDatabaseServiceConfiguration) error {
	if config == nil {
		return fmt.Errorf("got database service with no standard database service configuration")
	}

	hostname, port := u.fetchVariableFromSource(config.Hostname), int(config.Port)

	socket.UpstreamType = config.DatabaseProtocol
	socket.ConnectorData.TargetHostname = hostname
	socket.ConnectorData.Port = port
	socket.TargetHostname = hostname
	socket.TargetPort = port

	switch config.AuthenticationType {
	case service.DatabaseAuthenticationTypeUsernameAndPassword:
		if config.UsernameAndPasswordAuth == nil {
			return fmt.Errorf("got database service with no username and password authentication configuration")
		}
		socket.ConnectorLocalData.UpstreamUsername = u.fetchVariableFromSource(config.UsernameAndPasswordAuth.Username)
		socket.ConnectorLocalData.UpstreamPassword = u.fetchVariableFromSource(config.UsernameAndPasswordAuth.Password)
		socket.ConnectorLocalData.UpstreamTLS = pointer.To(false)
	case service.DatabaseAuthenticationTypeTls:
		if config.TlsAuth == nil {
			return fmt.Errorf("got database service with no TLS authentication configuration")
		}
		socket.ConnectorLocalData.UpstreamUsername = u.fetchVariableFromSource(config.TlsAuth.Username)
		socket.ConnectorLocalData.UpstreamPassword = u.fetchVariableFromSource(config.TlsAuth.Password)
		socket.ConnectorLocalData.UpstreamCertBlock = []byte(u.fetchVariableFromSource(config.TlsAuth.Certificate))
		socket.ConnectorLocalData.UpstreamKeyBlock = []byte(u.fetchVariableFromSource(config.TlsAuth.Key))
		if config.TlsAuth.CaCertificate != "" {
			socket.ConnectorLocalData.UpstreamCACertBlock = []byte(u.fetchVariableFromSource(config.TlsAuth.CaCertificate))
		}
		socket.ConnectorLocalData.UpstreamTLS = pointer.To(true)
	}

	return nil
}

func (u *UpstreamDataBuilder) buildUpstreamDataForDatabaseServiceAwsRds(socket *models.Socket, config *service.AwsRdsDatabaseServiceConfiguration) error {
	if config == nil {
		return fmt.Errorf("got database service with no AWS RDS database service configuration")
	}

	hostname, port := u.fetchVariableFromSource(config.Hostname), int(config.Port)

	socket.UpstreamType = config.DatabaseProtocol
	socket.ConnectorData.TargetHostname = hostname
	socket.ConnectorData.Port = port
	socket.TargetHostname = hostname
	socket.TargetPort = port

	switch config.AuthenticationType {
	case service.DatabaseAuthenticationTypeUsernameAndPassword:
		if config.UsernameAndPasswordAuth == nil {
			return fmt.Errorf("got database service with no username and password authentication configuration")
		}
		socket.ConnectorLocalData.UpstreamUsername = u.fetchVariableFromSource(config.UsernameAndPasswordAuth.Username)
		socket.ConnectorLocalData.UpstreamPassword = u.fetchVariableFromSource(config.UsernameAndPasswordAuth.Password)
		socket.ConnectorLocalData.UpstreamTLS = pointer.To(false)
		if config.UsernameAndPasswordAuth.CaCertificate != "" {
			socket.ConnectorLocalData.UpstreamCACertBlock = []byte(u.fetchVariableFromSource(config.UsernameAndPasswordAuth.CaCertificate))
			socket.ConnectorLocalData.UpstreamTLS = pointer.To(true)
		}
	case service.DatabaseAuthenticationTypeIam:
		if config.IamAuth == nil {
			return fmt.Errorf("got database service with no IAM authentication configuration")
		}
		socket.ConnectorLocalData.RdsIAMAuth = true
		socket.ConnectorLocalData.UpstreamTLS = pointer.To(true)
		socket.ConnectorLocalData.AWSRegion = config.IamAuth.AwsRegion
		socket.ConnectorLocalData.UpstreamUsername = u.fetchVariableFromSource(config.IamAuth.Username)
		if config.IamAuth.CaCertificate != "" {
			socket.ConnectorLocalData.UpstreamCACertBlock = []byte(u.fetchVariableFromSource(config.IamAuth.CaCertificate))
		}
	}

	return nil
}

func (u *UpstreamDataBuilder) buildUpstreamDataForDatabaseServiceGcpCloudSql(socket *models.Socket, config *service.GcpCloudSqlDatabaseServiceConfiguration) error {
	if config == nil {
		return fmt.Errorf("got database service with no Google Cloud SQL database service configuration")
	}

	if config.CloudSqlConnectorEnabled {
		return u.buildUpstreamDataForDatabaseServiceGcpCloudSqlConnector(socket, config.Connector)
	}

	return u.buildUpstreamDataForDatabaseServiceGcpCloudSqlStandard(socket, config.Standard)
}

func (u *UpstreamDataBuilder) buildUpstreamDataForDatabaseServiceGcpCloudSqlConnector(socket *models.Socket, config *service.GcpCloudSqlConnectorConfiguration) error {
	if config == nil {
		return fmt.Errorf("got database service with no Google Cloud SQL connector configuration")
	}

	socket.UpstreamType = config.DatabaseProtocol
	socket.ConnectorLocalData.CloudSQLConnector = true

	switch config.AuthenticationType {
	case service.DatabaseAuthenticationTypeUsernameAndPassword:
		if config.UsernameAndPasswordAuth == nil {
			return fmt.Errorf("got database service with no username and password authentication configuration")
		}
		socket.ConnectorLocalData.UpstreamUsername = u.fetchVariableFromSource(config.UsernameAndPasswordAuth.Username)
		socket.ConnectorLocalData.UpstreamPassword = u.fetchVariableFromSource(config.UsernameAndPasswordAuth.Password)
		socket.ConnectorLocalData.CloudSQLInstance = u.fetchVariableFromSource(config.UsernameAndPasswordAuth.InstanceId)
		socket.ConnectorLocalData.GoogleCredentialsJSON = []byte(u.fetchVariableFromSource(config.UsernameAndPasswordAuth.GcpCredentialsJson))
	case service.DatabaseAuthenticationTypeIam:
		if config.IamAuth == nil {
			return fmt.Errorf("got database service with no IAM authentication configuration")
		}
		socket.ConnectorLocalData.CloudSQLIAMAuth = true
		socket.ConnectorLocalData.CloudSQLInstance = u.fetchVariableFromSource(config.IamAuth.InstanceId)
		socket.ConnectorLocalData.GoogleCredentialsJSON = []byte(u.fetchVariableFromSource(config.IamAuth.GcpCredentialsJson))
	}

	return nil
}

func (u *UpstreamDataBuilder) buildUpstreamDataForDatabaseServiceGcpCloudSqlStandard(socket *models.Socket, config *service.GcpCloudSqlStandardConfiguration) error {
	if config == nil {
		return fmt.Errorf("got database service with no Google Cloud SQL standard configuration")
	}

	hostname, port := u.fetchVariableFromSource(config.Hostname), int(config.Port)

	socket.UpstreamType = config.DatabaseProtocol
	socket.ConnectorData.TargetHostname = hostname
	socket.ConnectorData.Port = port
	socket.TargetHostname = hostname
	socket.TargetPort = port

	switch config.AuthenticationType {
	case service.DatabaseAuthenticationTypeUsernameAndPassword:
		if config.UsernameAndPasswordAuth == nil {
			return fmt.Errorf("got database service with no username and password authentication configuration")
		}
		socket.ConnectorLocalData.UpstreamUsername = u.fetchVariableFromSource(config.UsernameAndPasswordAuth.Username)
		socket.ConnectorLocalData.UpstreamPassword = u.fetchVariableFromSource(config.UsernameAndPasswordAuth.Password)
	case service.DatabaseAuthenticationTypeTls:
		if config.TlsAuth == nil {
			return fmt.Errorf("got database service with no TLS authentication configuration")
		}
		socket.ConnectorLocalData.UpstreamUsername = u.fetchVariableFromSource(config.TlsAuth.Username)
		socket.ConnectorLocalData.UpstreamPassword = u.fetchVariableFromSource(config.TlsAuth.Password)
		socket.ConnectorLocalData.UpstreamCertBlock = []byte(u.fetchVariableFromSource(config.TlsAuth.Certificate))
		socket.ConnectorLocalData.UpstreamKeyBlock = []byte(u.fetchVariableFromSource(config.TlsAuth.Key))
		if config.TlsAuth.CaCertificate != "" {
			socket.ConnectorLocalData.UpstreamCACertBlock = []byte(u.fetchVariableFromSource(config.TlsAuth.CaCertificate))
		}
	}

	return nil
}
