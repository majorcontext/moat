package pi

import (
	"context"
	"errors"

	"github.com/majorcontext/moat/internal/provider"
)

// Provider implements provider.AgentProvider for the Pi coding agent.
//
// Pi has no credential of its own: it runs against whichever backend the user's
// anthropic or openai grant provides, so credential injection is handled by
// those credential providers, not here. This provider is purely the runtime —
// installing the CLI, staging the runtime context, and resolving which backend
// to launch.
type Provider struct{}

// Ensure Provider implements the required interfaces.
var (
	_ provider.CredentialProvider = (*Provider)(nil)
	_ provider.AgentProvider      = (*Provider)(nil)
)

func init() {
	provider.Register(&Provider{})
}

// Name returns the provider identifier.
func (p *Provider) Name() string { return "pi" }

// Grant always errors: Pi has no credential of its own. Users grant a model
// backend with `moat grant anthropic` or `moat grant openai` instead.
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	return nil, errors.New(
		"pi has no credential of its own — grant a model backend instead:\n" +
			"  Run: moat grant anthropic\n" +
			"  or:  moat grant openai")
}

// ConfigureProxy is a no-op: credential injection is delegated to the
// anthropic/openai credential providers for the resolved backend.
func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {}

// ContainerEnv is a no-op: the backend grant provider sets the placeholder
// API-key env var (ANTHROPIC_API_KEY / OPENAI_API_KEY) that Pi reads.
func (p *Provider) ContainerEnv(cred *provider.Credential) []string { return nil }

// ContainerMounts returns none — Pi uses the staging-directory approach
// (see PrepareContainer).
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

// Cleanup is a no-op — staging-directory cleanup is handled by the Cleanup
// closure returned from PrepareContainer.
func (p *Provider) Cleanup(cleanupPath string) {}

// ImpliedDependencies returns none.
func (p *Provider) ImpliedDependencies() []string { return nil }

// PrepareContainer is implemented in agent.go; RegisterCLI in cli.go.
