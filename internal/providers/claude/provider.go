package claude

import (
	"github.com/majorcontext/moat/internal/provider"
)

// Provider implements provider.CredentialProvider and provider.AgentProvider
// for Claude Code / Anthropic credentials.
type Provider struct{}

// Ensure Provider implements the required interfaces.
var (
	_ provider.CredentialProvider = (*Provider)(nil)
	_ provider.AgentProvider      = (*Provider)(nil)
)

func init() {
	provider.Register(&Provider{})
	// Register "anthropic" as an alias so credentials stored under either name work
	provider.RegisterAlias("anthropic", "claude")
}

// Name returns the provider identifier.
func (p *Provider) Name() string {
	return "claude"
}

// ConfigureProxy sets up proxy headers for Anthropic API.
func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	if isOAuthToken(cred.Token) {
		// OAuth token - use Bearer auth with the real token
		// The proxy injects this at the network layer
		proxy.SetCredentialWithGrant("api.anthropic.com", "Authorization", "Bearer "+cred.Token, "anthropic")

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
	} else {
		// Standard API key - use x-api-key header
		proxy.SetCredentialWithGrant("api.anthropic.com", "x-api-key", cred.Token, "anthropic")
	}
}

// ContainerEnv returns environment variables for Anthropic.
func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	if cred == nil {
		// No credential - return placeholder that tells Claude Code it's authenticated
		// The proxy will inject real credentials at the network layer
		return []string{"ANTHROPIC_API_KEY=" + ProxyInjectedPlaceholder}
	}
	if isOAuthToken(cred.Token) {
		// For OAuth tokens, set CLAUDE_CODE_OAUTH_TOKEN with a placeholder.
		// This tells Claude Code it's authenticated (skips login prompts).
		// The real token is injected by the proxy at the network layer.
		return []string{"CLAUDE_CODE_OAUTH_TOKEN=" + ProxyInjectedPlaceholder}
	}
	// For API keys, set a placeholder so Claude Code doesn't error
	// The real key is injected by the proxy at the network layer
	return []string{"ANTHROPIC_API_KEY=" + ProxyInjectedPlaceholder}
}

// ContainerMounts returns mounts needed for Claude Code.
// This method returns empty because Claude Code setup uses the staging directory
// approach instead of direct mounts. The staging directory is populated by
// PrepareContainer and copied to the container at startup by moat-init.
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	// No direct mounts - we use the staging directory approach instead
	return nil, "", nil
}

// Cleanup cleans up Claude resources.
func (p *Provider) Cleanup(cleanupPath string) {
	// Nothing to clean up - staging directory is handled by the caller
}

// ImpliedDependencies returns dependencies implied by the Claude provider.
// Claude Code requires node.js runtime.
func (p *Provider) ImpliedDependencies() []string {
	return nil
}

// isOAuthToken returns true if the token appears to be a Claude Code OAuth token.
//
// This uses a prefix-based heuristic: OAuth tokens from Claude Code start with
// "sk-ant-oat" (Anthropic OAuth Token). This prefix format is based on observed
// token structure as of 2025. If Anthropic changes their token format in the
// future, this function may need to be updated.
//
// Note: API keys typically start with "sk-ant-api" for comparison.
func isOAuthToken(token string) bool {
	return len(token) > 10 && token[:10] == "sk-ant-oat"
}
