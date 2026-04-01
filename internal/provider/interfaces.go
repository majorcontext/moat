package provider

import (
	"context"
	"net/http"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/spf13/cobra"
)

// RunStoppedContext provides run information to shutdown hooks.
type RunStoppedContext struct {
	Workspace string
	StartedAt time.Time
}

// RunStoppedHook is an optional interface for providers that need to perform
// actions after a run stops. The manager calls OnRunStopped for each grant
// provider that implements this interface.
type RunStoppedHook interface {
	// OnRunStopped is called after the container exits and logs are captured.
	// It receives run context and returns metadata key-value pairs to persist.
	// Returned metadata is stored in the run's metadata.json under "provider_meta".
	OnRunStopped(ctx RunStoppedContext) map[string]string
}

// ProxyConfigurer configures proxy credentials and response transformations.
// This is an alias for credential.ProxyConfigurer to ensure type compatibility.
type ProxyConfigurer = credential.ProxyConfigurer

// ResponseTransformer modifies HTTP responses for a host.
// This is an alias for credential.ResponseTransformer to ensure type compatibility.
type ResponseTransformer = credential.ResponseTransformer

// ProxyProvider configures proxy credential injection for a given credential.
// This is the core interface Gate Keeper needs from a provider.
type ProxyProvider interface {
	// Name returns the provider identifier (e.g., "github", "claude").
	Name() string

	// ConfigureProxy sets up proxy headers for this credential.
	ConfigureProxy(p ProxyConfigurer, cred *Credential)
}

// GrantProvider acquires credentials interactively or from environment.
// Used by moat CLI — Gate Keeper never calls this.
type GrantProvider interface {
	Grant(ctx context.Context) (*Credential, error)
}

// ContainerProvider sets up the container environment for a credential.
// Used by moat CLI — Gate Keeper doesn't manage containers.
type ContainerProvider interface {
	// ContainerEnv returns environment variables to set in the container.
	ContainerEnv(cred *Credential) []string

	// ContainerMounts returns mounts needed for this credential.
	// Also returns an optional cleanup path that should be passed to Cleanup()
	// when the run ends.
	ContainerMounts(cred *Credential, containerHome string) ([]MountConfig, string, error)

	// Cleanup is called when the run ends to clean up any resources.
	Cleanup(cleanupPath string)
}

// ImpliedDepsProvider declares dependencies between providers.
// Used by moat CLI.
type ImpliedDepsProvider interface {
	// ImpliedDependencies returns dependencies implied by this provider.
	// For example, github implies ["gh", "git"].
	ImpliedDependencies() []string
}

// CredentialProvider is the composite interface for full provider implementations.
// All current providers implement this. Gate Keeper only requires ProxyProvider.
type CredentialProvider interface {
	ProxyProvider
	GrantProvider
	ContainerProvider
	ImpliedDepsProvider
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

// InitFileProvider is an optional interface for providers that need config
// files written into the container at startup. The run manager collects
// init files from all providers and passes them to moat-init.sh via the
// MOAT_INIT_FILES env var. This avoids bind-mounting config directories
// that the tool needs to write to at runtime.
//
// Use InitFileProvider (not ContainerMounts) when the tool needs to write
// to its config directory at runtime. Bind mounts prevent writes to the
// mount target, causing silent tool failures.
type InitFileProvider interface {
	// ContainerInitFiles returns a map of absolute container paths to file
	// contents. Paths must be under containerHome. The moat-init.sh script
	// writes each entry to disk with mode 0600 before executing the user's
	// command.
	ContainerInitFiles(cred *Credential, containerHome string) map[string]string
}

// EndpointProvider exposes HTTP endpoints to containers.
// Implemented by aws for the credential endpoint.
type EndpointProvider interface {
	CredentialProvider

	// RegisterEndpoints registers HTTP handlers on the proxy mux.
	RegisterEndpoints(mux *http.ServeMux, cred *Credential)
}
