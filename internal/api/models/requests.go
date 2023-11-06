package models

type LoginResponse struct {
	Token string `json:"token"`
	MFA   bool   `json:"require_mfa"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type RegisterForm struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Sshkey   string `json:"sshkey"`
}
type LoginForm struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type MfaForm struct {
	Code string `json:"code"`
}

type LoginRefresh struct {
}

type TokenForm struct {
	Token string `json:"token"`
	MFA   bool   `json:"require_mfa"`
}

type SessionTokenForm struct {
	Token string `json:"token"`
	MFA   bool   `json:"require_mfa"`
	State string `json:"state"`
}

type SwitchOrgRequest struct {
	OrgName string `json:"org_name"`
}

type SwitchOrgResponse struct {
	Token   string `json:"token"`
	OrgName string `json:"org_name"`
	OrgID   string `json:"org_id"`
}

type SshCsr struct {
	SSHPublicKey  string `json:"ssh_public_key"`
	SSHSignedCert string `json:"signed_ssh_cert,omitempty"`
	HostKey       string `json:"host_key,omitempty"`
}

type EvaluatePolicyRequest struct {
	ClientIP   string `json:"client_ip"`
	UserEmail  string `json:"user_email"`
	SessionKey string `json:"session_key"`
}

type EvaluatePolicyResponse struct {
	Actions map[string][]string `json:"allowed_actions"`
	Info    map[string][]string `json:"info"`
}

type UpdateSessionRequest struct {
	UserData   string `json:"user_data"`
	SessionKey string `json:"session_key"`
}

type SignSshOrgCertificateRequest struct {
	SocketID   string `json:"socket_id"`
	SessionKey string `json:"session_key"`
	UserEmail  string `json:"user_email"`
	Ticket     string `json:"ticket"`
	PublicKey  string `json:"public_key"`
}

type SignSshOrgCertificateResponse struct {
	Certificate string `json:"certificate"`
}
