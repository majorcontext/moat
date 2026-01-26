package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/andybons/moat/internal/container"
	"github.com/andybons/moat/internal/credential"
)

// OAuthBetaHeader is the Anthropic beta header required for OAuth authentication.
const OAuthBetaHeader = "oauth-2025-04-20"

// AnthropicSetup implements credential.ProviderSetup for Anthropic credentials.
// It handles both OAuth tokens (from Claude Code) and API keys.
type AnthropicSetup struct{}

// Provider returns the provider identifier.
func (a *AnthropicSetup) Provider() credential.Provider {
	return credential.ProviderAnthropic
}

// ConfigureProxy sets up proxy headers for Anthropic API.
func (a *AnthropicSetup) ConfigureProxy(p credential.ProxyConfigurer, cred *credential.Credential) {
	if credential.IsOAuthToken(cred.Token) {
		// OAuth token - use Bearer auth with the real token
		// The proxy injects this at the network layer
		// Also requires anthropic-beta header for OAuth support
		p.SetCredential("api.anthropic.com", "Bearer "+cred.Token)
		p.AddExtraHeader("api.anthropic.com", "anthropic-beta", OAuthBetaHeader)
	} else {
		// Standard API key - use x-api-key header
		p.SetCredentialHeader("api.anthropic.com", "x-api-key", cred.Token)
	}
}

// ContainerEnv returns environment variables for Anthropic.
func (a *AnthropicSetup) ContainerEnv(cred *credential.Credential) []string {
	if credential.IsOAuthToken(cred.Token) {
		// For OAuth tokens, set CLAUDE_CODE_OAUTH_TOKEN with a placeholder.
		// This tells Claude Code it's authenticated (skips login prompts).
		// The real token is injected by the proxy at the network layer.
		return []string{"CLAUDE_CODE_OAUTH_TOKEN=" + credential.ProxyInjectedPlaceholder}
	}
	// For API keys, set a placeholder so Claude Code doesn't error
	// The real key is injected by the proxy at the network layer
	return []string{"ANTHROPIC_API_KEY=" + credential.ProxyInjectedPlaceholder}
}

// ContainerMounts returns mounts needed for Anthropic/Claude Code.
// This method returns empty because Claude Code setup uses the staging directory
// approach instead of direct mounts. The staging directory is populated by
// PopulateStagingDir and copied to the container at startup by moat-init.
func (a *AnthropicSetup) ContainerMounts(cred *credential.Credential, containerHome string) ([]container.MountConfig, string, error) {
	// No direct mounts - we use the staging directory approach instead
	return nil, "", nil
}

// Cleanup cleans up Anthropic resources.
func (a *AnthropicSetup) Cleanup(cleanupPath string) {
	// Nothing to clean up - staging directory is handled by the caller
}

// PopulateStagingDir populates the Claude staging directory with OAuth credentials.
// This should only be called for OAuth tokens - API keys don't need credential files.
//
// Files added:
// - .credentials.json (placeholder token - real auth is via proxy)
//
// SECURITY: The real OAuth token is NEVER written to the container filesystem.
// Authentication is handled by the TLS-intercepting proxy at the network layer.
//
// Note: Use WriteClaudeConfig to write the .claude.json config file.
func (a *AnthropicSetup) PopulateStagingDir(cred *credential.Credential, stagingDir string) error {
	if !credential.IsOAuthToken(cred.Token) {
		// API keys don't need credential files
		return nil
	}

	// Write credentials file with a placeholder token.
	// The real token is NEVER written to the container - it's injected by
	// the proxy at the network layer. Claude Code needs this file to exist
	// with valid structure to function, but the actual authentication is
	// handled transparently by the TLS-intercepting proxy.
	creds := credential.ClaudeOAuthCredentials{
		ClaudeAiOauth: &credential.ClaudeOAuthToken{
			AccessToken: credential.ProxyInjectedPlaceholder,
			ExpiresAt:   cred.ExpiresAt.UnixMilli(),
			Scopes:      cred.Scopes,
		},
	}

	credsJSON, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("marshaling credentials: %w", err)
	}

	if writeErr := os.WriteFile(filepath.Join(stagingDir, ".credentials.json"), credsJSON, 0600); writeErr != nil {
		return fmt.Errorf("writing credentials file: %w", writeErr)
	}

	return nil
}

// MCPServerForContainer represents an MCP server in Claude's .claude.json format.
type MCPServerForContainer struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// WriteClaudeConfig writes a minimal ~/.claude.json to the staging directory.
// This skips the onboarding flow, sets dark theme, and optionally configures MCP servers.
// mcpServers is a map of server names to their configurations.
func WriteClaudeConfig(stagingDir string, mcpServers map[string]MCPServerForContainer) error {
	config := map[string]any{
		"hasCompletedOnboarding": true,
		"theme":                  "dark",
	}

	// Add MCP servers if provided
	if len(mcpServers) > 0 {
		config["mcpServers"] = mcpServers
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling claude config: %w", err)
	}

	if err := os.WriteFile(filepath.Join(stagingDir, ".claude.json"), data, 0644); err != nil {
		return fmt.Errorf("writing .claude.json: %w", err)
	}

	return nil
}

// init registers the Anthropic provider setup.
func init() {
	credential.RegisterProviderSetup(credential.ProviderAnthropic, &AnthropicSetup{})
}
