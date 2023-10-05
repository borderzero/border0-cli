package models

import (
	"time"

	"github.com/borderzero/border0-go/types/connector"
	"github.com/borderzero/border0-go/types/service"
)

// ConnectorList represents a list of connectors
type ConnectorList struct {
	List []Connector `json:"list"`
}

// Connector represents a cloud-managed Border0 Connector.
type Connector struct {
	Name                           string                                  `json:"name"`
	ConnectorID                    string                                  `json:"connector_id"`
	BuiltInSshServiceEnabled       bool                                    `json:"built_in_ssh_service_enabled"`
	BuiltInSshServiceConfiguration *service.BuiltInSshServiceConfiguration `json:"built_in_ssh_service_configuration,omitempty"`
	Description                    string                                  `json:"description"`
	ActiveTokens                   int                                     `json:"active_tokens"`
	Metadata                       map[string]interface{}                  `json:"metadata"`
	CreatedAt                      *time.Time                              `json:"created_at"`
	UpdatedAt                      *time.Time                              `json:"updated_at"`
	LastSeenAt                     *time.Time                              `json:"last_seen_at"`
}

// ConnectorTokenRequest represents a request to create a token for a Border0 Connector.
type ConnectorTokenRequest struct {
	ConnectorId string `json:"connector_id,omitempty"`
	Name        string `json:"name,omitempty"`
	ExpiresAt   int64  `json:"expires_at,omitempty"`
}

// ConnectorToken represents a token for a Border0 Connector.
type ConnectorToken struct {
	ConnectorName string `json:"connector_name,omitempty"`
	Name          string `json:"name,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	Token         string `json:"token,omitempty"`
}

// ConnectorPluginRequest represents a request to create a plugin for a Border0 Connector.
type ConnectorPluginRequest struct {
	ConnectorId   string                         `json:"connector_id"`
	Enabled       bool                           `json:"enabled"`
	PluginType    string                         `json:"plugin_type"`
	Configuration *connector.PluginConfiguration `json:"configuration"`
}

// ConnectorPlugin represents a plugin for a Border0 Connector.
type ConnectorPlugin struct {
	ID            string                        `json:"id"`
	Enabled       bool                          `json:"enabled"`
	PluginType    string                        `json:"plugin_type"`
	Configuration connector.PluginConfiguration `json:"configuration"`
}

// ConnectorAutoCreationRuleRequest represents a request to create an autocreation rule for a Border0 Connector.
type ConnectorAutoCreationRuleRequest struct {
	ConnectorId  string                   `json:"connector_id"`
	Enabled      bool                     `json:"enabled"`
	ResourceType string                   `json:"resource_type"`
	Rules        []map[string]interface{} `json:"rules"`
}

// ConnectorAutoCreationRule represents an autocreation rule for a Border0 Connector.
type ConnectorAutoCreationRule struct {
	ID           string                   `json:"id"`
	Enabled      bool                     `json:"enabled"`
	ResourceType string                   `json:"resource_type"`
	Rules        []map[string]interface{} `json:"rules"`
}
