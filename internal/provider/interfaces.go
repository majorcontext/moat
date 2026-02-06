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

	// CanRefresh reports whether this credential can be refreshed.
	CanRefresh(cred *Credential) bool

	// RefreshInterval returns how often to attempt refresh.
	// Returns 0 if refresh is not supported.
	RefreshInterval() time.Duration

	// Refresh re-acquires a fresh token and updates the proxy.
	// Returns ErrRefreshNotSupported if the credential cannot be refreshed.
	Refresh(ctx context.Context, p ProxyConfigurer, cred *Credential) (*Credential, error)

	// Cleanup is called when the run ends to clean up any resources.
	Cleanup(cleanupPath string)

	// ImpliedDependencies returns dependencies implied by this provider.
	// For example, github implies ["gh", "git"].
	ImpliedDependencies() []string
}

// AgentProvider extends CredentialProvider for AI agent runtimes.
// Implemented by claude and codex providers.
type AgentProvider interface {
	CredentialProvider

	// PrepareContainer sets up staging directories and config files.
	PrepareContainer(ctx context.Context, opts PrepareOpts) (*ContainerConfig, error)

	// Sessions returns all sessions for this agent.
	Sessions() ([]Session, error)

	// ResumeSession resumes an existing session by ID.
	ResumeSession(id string) error

	// RegisterCLI adds provider-specific commands to the root command.
	RegisterCLI(root *cobra.Command)
}

// EndpointProvider exposes HTTP endpoints to containers.
// Implemented by aws for the credential endpoint.
type EndpointProvider interface {
	CredentialProvider

	// RegisterEndpoints registers HTTP handlers on the proxy mux.
	RegisterEndpoints(mux *http.ServeMux, cred *Credential)
}
