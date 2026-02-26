package daemon

import (
	"github.com/majorcontext/moat/internal/config"
)

// CredentialSpec describes a credential to inject for a host.
type CredentialSpec struct {
	Host   string `json:"host"`
	Header string `json:"header"`
	Value  string `json:"value"`
	Grant  string `json:"grant,omitempty"`
}

// ExtraHeaderSpec describes an additional header to inject.
type ExtraHeaderSpec struct {
	Host       string `json:"host"`
	HeaderName string `json:"header_name"`
	Value      string `json:"value"`
}

// TokenSubstitutionSpec describes a token substitution.
type TokenSubstitutionSpec struct {
	Host        string `json:"host"`
	Placeholder string `json:"placeholder"`
	RealToken   string `json:"real_token"`
}

// RemoveHeaderSpec describes a header to remove from requests.
type RemoveHeaderSpec struct {
	Host       string `json:"host"`
	HeaderName string `json:"header_name"`
}

// RegisterRequest is sent to POST /v1/runs.
type RegisterRequest struct {
	RunID              string                   `json:"run_id"`
	Credentials        []CredentialSpec         `json:"credentials,omitempty"`
	ExtraHeaders       []ExtraHeaderSpec        `json:"extra_headers,omitempty"`
	RemoveHeaders      []RemoveHeaderSpec       `json:"remove_headers,omitempty"`
	TokenSubstitutions []TokenSubstitutionSpec  `json:"token_substitutions,omitempty"`
	MCPServers         []config.MCPServerConfig `json:"mcp_servers,omitempty"`
	NetworkPolicy      string                   `json:"network_policy,omitempty"`
	NetworkAllow       []string                 `json:"network_allow,omitempty"`
	Grants             []string                 `json:"grants,omitempty"`
	AWSConfig          *AWSConfig               `json:"aws_config,omitempty"`
}

// RegisterResponse is returned from POST /v1/runs.
type RegisterResponse struct {
	AuthToken string `json:"auth_token"`
	ProxyPort int    `json:"proxy_port"`
}

// UpdateRunRequest is sent to PATCH /v1/runs/{token}.
type UpdateRunRequest struct {
	ContainerID string `json:"container_id"`
}

// HealthResponse is returned from GET /v1/health.
type HealthResponse struct {
	PID       int    `json:"pid"`
	ProxyPort int    `json:"proxy_port"`
	RunCount  int    `json:"run_count"`
	StartedAt string `json:"started_at"`
}

// RunInfo is an element of the list returned by GET /v1/runs.
type RunInfo struct {
	RunID        string `json:"run_id"`
	ContainerID  string `json:"container_id,omitempty"`
	RegisteredAt string `json:"registered_at"`
}

// RouteRegistration is sent to POST /v1/routes/{agent}.
type RouteRegistration struct {
	Services map[string]string `json:"services"`
}

// ToRunContext converts a RegisterRequest into a RunContext.
func (req *RegisterRequest) ToRunContext() *RunContext {
	rc := NewRunContext(req.RunID)
	for _, c := range req.Credentials {
		rc.SetCredentialWithGrant(c.Host, c.Header, c.Value, c.Grant)
	}
	for _, h := range req.ExtraHeaders {
		rc.AddExtraHeader(h.Host, h.HeaderName, h.Value)
	}
	for _, r := range req.RemoveHeaders {
		rc.RemoveRequestHeader(r.Host, r.HeaderName)
	}
	for _, ts := range req.TokenSubstitutions {
		rc.SetTokenSubstitution(ts.Host, ts.Placeholder, ts.RealToken)
	}
	rc.MCPServers = req.MCPServers
	rc.NetworkPolicy = req.NetworkPolicy
	rc.NetworkAllow = req.NetworkAllow
	rc.AWSConfig = req.AWSConfig
	return rc
}
