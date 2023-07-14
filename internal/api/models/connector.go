package models

import (
	"time"
)

// Connector represents a cloud-managed Border0 Connector.
type Connector struct {
	Name         string                 `json:"name"`
	ConnectorID  string                 `json:"connector_id"`
	Description  string                 `json:"description"`
	ActiveTokens int                    `json:"active_tokens"`
	Metadata     map[string]interface{} `json:"metadata"`
	CreatedAt    *time.Time             `json:"created_at"`
	UpdatedAt    *time.Time             `json:"updated_at"`
	LastSeenAt   *time.Time             `json:"last_seen_at"`
}

// ConnectorTokenRequest represents a request to create a token for a Border0 Connector.
type ConnectorTokenRequest struct {
	ConnectorName string `json:"connector_name,omitempty"`
	Name          string `json:"name,omitempty"`
	ExpiresAt     int64  `json:"expires_at,omitempty"`
}

// ConnectorToken represents a token for a Border0 Connector.
type ConnectorToken struct {
	ConnectorName string `json:"connector_name,omitempty"`
	Name          string `json:"name,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	Token         string `json:"token,omitempty"`
}
