package models

type Token struct {
	ExpiresAt int    `json:"expires_at,omitempty"`
	Name      string `json:"name,omitempty"`
	Role      string `json:"role,omitempty"`
	Token     string `json:"token,omitempty"`
}
