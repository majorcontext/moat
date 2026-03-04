package provider

import (
	"time"

	"github.com/majorcontext/moat/internal/container"
)

// MetaKeyTokenSource is the metadata key for recording how a token was obtained.
const MetaKeyTokenSource = "token_source"

// Credential represents a stored credential.
type Credential struct {
	Provider  string            `json:"provider"`
	Token     string            `json:"token"`
	Scopes    []string          `json:"scopes,omitempty"`
	ExpiresAt time.Time         `json:"expires_at,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// MountConfig re-exports container.MountConfig for provider use.
type MountConfig = container.MountConfig

// PrepareOpts contains options for AgentProvider.PrepareContainer.
type PrepareOpts struct {
	Credential    *Credential
	ContainerHome string
	MCPServers    map[string]MCPServerConfig
	HostConfig    map[string]interface{}
}

// MCPServerConfig defines an MCP server configuration.
type MCPServerConfig struct {
	URL     string
	Headers map[string]string
}

// ContainerConfig is returned by AgentProvider.PrepareContainer.
type ContainerConfig struct {
	Env        []string
	Mounts     []MountConfig
	StagingDir string // Temporary directory containing config files (for later cleanup tracking)
	Cleanup    func()
}

// LegacyCredential is an interface for converting from credential.Credential.
// This avoids import cycles between provider and credential packages.
type LegacyCredential interface {
	GetProvider() string
	GetToken() string
	GetScopes() []string
	GetExpiresAt() time.Time
	GetCreatedAt() time.Time
	GetMetadata() map[string]string
}

// FromLegacy converts a LegacyCredential (like credential.Credential) to provider.Credential.
func FromLegacy(cred LegacyCredential) *Credential {
	if cred == nil {
		return nil
	}
	return &Credential{
		Provider:  cred.GetProvider(),
		Token:     cred.GetToken(),
		Scopes:    cred.GetScopes(),
		ExpiresAt: cred.GetExpiresAt(),
		CreatedAt: cred.GetCreatedAt(),
		Metadata:  cred.GetMetadata(),
	}
}
