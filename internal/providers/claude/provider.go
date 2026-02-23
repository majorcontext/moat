package claude

import (
	"github.com/majorcontext/moat/internal/provider"
)

// OAuthProvider implements provider.CredentialProvider and provider.AgentProvider
// for Claude Code OAuth tokens (from Claude Pro/Max subscriptions).
//
// OAuth tokens are restricted by Anthropic's ToS to Claude Code and Claude.ai only.
// This provider uses Bearer auth with the required beta flag and response transformer.
type OAuthProvider struct{}

// AnthropicProvider implements provider.CredentialProvider for Anthropic API keys.
//
// API keys work with any tool or agent — they are not restricted to Claude Code.
// This provider uses the x-api-key header with no OAuth workarounds.
type AnthropicProvider struct{}

// Ensure providers implement the required interfaces.
var (
	_ provider.CredentialProvider = (*OAuthProvider)(nil)
	_ provider.AgentProvider      = (*OAuthProvider)(nil)
	_ provider.CredentialProvider = (*AnthropicProvider)(nil)
)

func init() {
	provider.Register(&OAuthProvider{})
	provider.Register(&AnthropicProvider{})
}

// --- OAuthProvider ---

// Name returns the provider identifier.
func (p *OAuthProvider) Name() string {
	return "claude"
}

// ConfigureProxy sets up proxy headers for OAuth tokens on the Anthropic API.
func (p *OAuthProvider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	// OAuth token - use Bearer auth with the real token
	proxy.SetCredentialWithGrant("api.anthropic.com", "Authorization", "Bearer "+cred.Token, "claude")

	// Strip any client-sent x-api-key header — it conflicts with the
	// injected Authorization header and Anthropic rejects requests that
	// have an invalid x-api-key even when Authorization is valid.
	proxy.RemoveRequestHeader("api.anthropic.com", "x-api-key")

	// OAuth tokens require the beta flag to be accepted by the API.
	// Without this, the API returns "OAuth authentication is currently
	// not supported."
	proxy.AddExtraHeader("api.anthropic.com", "anthropic-beta", "oauth-2025-04-20")

	// Register response transformer to handle 403s on OAuth endpoints
	// that require scopes not available in long-lived tokens
	proxy.AddResponseTransformer("api.anthropic.com", CreateOAuthEndpointTransformer())
}

// ContainerEnv returns environment variables for OAuth token injection.
func (p *OAuthProvider) ContainerEnv(cred *provider.Credential) []string {
	// Set CLAUDE_CODE_OAUTH_TOKEN with a placeholder.
	// This tells Claude Code it's authenticated (skips login prompts).
	// The real token is injected by the proxy at the network layer.
	return []string{"CLAUDE_CODE_OAUTH_TOKEN=" + ProxyInjectedPlaceholder}
}

// ContainerMounts returns mounts needed for Claude Code.
// This method returns empty because Claude Code setup uses the staging directory
// approach instead of direct mounts.
func (p *OAuthProvider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

// Cleanup cleans up Claude resources.
func (p *OAuthProvider) Cleanup(cleanupPath string) {}

// ImpliedDependencies returns dependencies implied by the Claude OAuth provider.
func (p *OAuthProvider) ImpliedDependencies() []string {
	return nil
}

// --- AnthropicProvider ---

// Name returns the provider identifier.
func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

// ConfigureProxy sets up proxy headers for API keys on the Anthropic API.
func (p *AnthropicProvider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	proxy.SetCredentialWithGrant("api.anthropic.com", "x-api-key", cred.Token, "anthropic")
}

// ContainerEnv returns environment variables for API key injection.
func (p *AnthropicProvider) ContainerEnv(cred *provider.Credential) []string {
	// Set a placeholder so Claude Code doesn't error
	// The real key is injected by the proxy at the network layer
	return []string{"ANTHROPIC_API_KEY=" + ProxyInjectedPlaceholder}
}

// ContainerMounts returns mounts needed for the Anthropic provider (none).
func (p *AnthropicProvider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

// Cleanup cleans up Anthropic resources.
func (p *AnthropicProvider) Cleanup(cleanupPath string) {}

// ImpliedDependencies returns dependencies implied by the Anthropic provider.
func (p *AnthropicProvider) ImpliedDependencies() []string {
	return nil
}
