package provider

import (
	"context"
	"net/http"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/spf13/cobra"
)

// ProxyConfigurer configures proxy credentials and response transformations.
// This is an alias for credential.ProxyConfigurer to ensure type compatibility.
type ProxyConfigurer = credential.ProxyConfigurer

// ResponseTransformer modifies HTTP responses for a host.
// This is an alias for credential.ResponseTransformer to ensure type compatibility.
type ResponseTransformer = credential.ResponseTransformer

// CredentialProvider is implemented by all providers.
// Handles credential acquisition, proxy configuration, and container setup.
type CredentialProvider interface {
	// Name returns the provider identifier (e.g., "github", "claude").
	Name() string

	// Grant acquires credentials interactively or from environment.
	Grant(ctx context.Context) (*Credential, error)

	// ConfigureProxy sets up proxy headers for this credential.
	ConfigureProxy(p ProxyConfigurer, cred *Credential)

	// ContainerEnv returns environment variables to set in the container.
	ContainerEnv(cred *Credential) []string

	// ContainerMounts returns mounts needed for this credential.
	// Also returns an optional cleanup path that should be passed to Cleanup()
	// when the run ends.
	ContainerMounts(cred *Credential, containerHome string) ([]MountConfig, string, error)

	// Cleanup is called when the run ends to clean up any resources.
	Cleanup(cleanupPath string)

	// ImpliedDependencies returns dependencies implied by this provider.
	// For example, github implies ["gh", "git"].
	ImpliedDependencies() []string
}

// RefreshableProvider is an optional interface for providers that support
// background credential refresh. Providers with static credentials
// (API keys, role ARNs) do not implement this.
type RefreshableProvider interface {
	CanRefresh(cred *Credential) bool
	RefreshInterval() time.Duration
	Refresh(ctx context.Context, p ProxyConfigurer, cred *Credential) (*Credential, error)
}

// AgentProvider extends CredentialProvider for AI agent runtimes.
// Implemented by claude, codex, and gemini providers.
type AgentProvider interface {
	CredentialProvider

	// PrepareContainer sets up staging directories and config files.
	PrepareContainer(ctx context.Context, opts PrepareOpts) (*ContainerConfig, error)

	// RegisterCLI adds provider-specific commands to the root command.
	RegisterCLI(root *cobra.Command)
}

// DescribableProvider is an optional interface for providers that describe
// themselves in listings like 'moat grant providers'.
type DescribableProvider interface {
	Description() string
	Source() string // "builtin" or "custom"
}

// EndpointProvider exposes HTTP endpoints to containers.
// Implemented by aws for the credential endpoint.
type EndpointProvider interface {
	CredentialProvider

	// RegisterEndpoints registers HTTP handlers on the proxy mux.
	RegisterEndpoints(mux *http.ServeMux, cred *Credential)
}
