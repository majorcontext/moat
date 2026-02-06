package codex

import (
	"context"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

// Provider implements provider.CredentialProvider and provider.AgentProvider
// for OpenAI Codex CLI credentials.
type Provider struct{}

// Ensure Provider implements the required interfaces.
var (
	_ provider.CredentialProvider = (*Provider)(nil)
	_ provider.AgentProvider      = (*Provider)(nil)
)

func init() {
	provider.Register(&Provider{})
	// Register "openai" as an alias so credentials stored under either name work
	provider.RegisterAlias("openai", "codex")
}

// Name returns the provider identifier.
func (p *Provider) Name() string {
	return "codex"
}

// Grant acquires OpenAI credentials interactively or from environment.
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	g := NewGrant()
	cred, err := g.Execute(ctx)
	if err != nil {
		return nil, err
	}
	return cred, nil
}

// ConfigureProxy sets up proxy headers for OpenAI API.
// The proxy intercepts requests to api.openai.com and injects the
// Authorization header with the real API key.
func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	// OpenAI uses Bearer token authentication for API keys
	proxy.SetCredentialWithGrant("api.openai.com", "Authorization", "Bearer "+cred.Token, "codex")
}

// ContainerEnv returns environment variables for OpenAI.
// Sets OPENAI_API_KEY with a placeholder that looks like a valid API key.
// This tells Codex CLI it's authenticated (skips login prompts) and
// bypasses local format validation.
// The real token is injected by the proxy at the network layer.
func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	return []string{"OPENAI_API_KEY=" + OpenAIAPIKeyPlaceholder}
}

// ContainerMounts returns mounts needed for OpenAI/Codex.
// This method returns empty because Codex setup uses the staging directory
// approach instead of direct mounts. The staging directory is populated by
// PrepareContainer and copied to the container at startup by moat-init.
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	// No direct mounts - we use the staging directory approach instead
	return nil, "", nil
}

// CanRefresh reports whether this credential can be refreshed.
// OpenAI API keys are static and cannot be refreshed.
func (p *Provider) CanRefresh(cred *provider.Credential) bool {
	return false
}

// RefreshInterval returns how often to attempt refresh.
// Returns 0 since OpenAI API keys don't support refresh.
func (p *Provider) RefreshInterval() time.Duration {
	return 0
}

// Refresh attempts to refresh the credential.
// Always returns an error since OpenAI API keys don't support refresh.
func (p *Provider) Refresh(ctx context.Context, proxy provider.ProxyConfigurer, cred *provider.Credential) (*provider.Credential, error) {
	return nil, provider.ErrRefreshNotSupported
}

// Cleanup cleans up OpenAI resources.
func (p *Provider) Cleanup(cleanupPath string) {
	// Nothing to clean up - staging directory is handled by the caller
}

// ImpliedDependencies returns dependencies implied by the Codex provider.
// Codex doesn't imply any specific tool dependencies.
func (p *Provider) ImpliedDependencies() []string {
	return nil
}
