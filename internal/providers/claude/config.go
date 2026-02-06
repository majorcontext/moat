package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/provider"
)

// ProxyInjectedPlaceholder is a placeholder value for credentials that will be
// injected by the Moat proxy at runtime.
const ProxyInjectedPlaceholder = "moat-proxy-injected"

// ClaudeInitMountPath is the path where the Claude staging directory is mounted.
// The moat-init script reads from this path and copies files to ~/.claude.
const ClaudeInitMountPath = "/moat/claude-init"

// ClaudePluginsPath is the base path for Claude plugins in the container.
// This matches Claude Code's expected location at ~/.claude/plugins.
// We use the absolute path for moatuser since that's our standard container user.
const ClaudePluginsPath = "/home/moatuser/.claude/plugins"

// ClaudeMarketplacesPath is the path where marketplaces are mounted in the container.
const ClaudeMarketplacesPath = ClaudePluginsPath + "/marketplaces"

// MCPServerForContainer represents an MCP server in Claude's .claude.json format.
type MCPServerForContainer struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// HostConfigAllowlist lists fields from the host's ~/.claude.json that are
// safe and useful to copy into containers. These avoid startup API calls
// and ensure consistent behavior.
//
// Fields are categorized by purpose:
//   - OAuth authentication: oauthAccount, userID, anonymousId (required for x-organization-uuid header)
//   - Installation tracking: installMethod, lastOnboardingVersion, numStartups (affects auth behavior)
//   - Feature flags: migration flags, clientDataCache (contains system_prompt_variant)
//   - Performance: cachedGrowthBookFeatures (optional, reduces startup API calls)
var HostConfigAllowlist = []string{
	// OAuth authentication fields (CRITICAL - missing these causes auth failures)
	"oauthAccount",  // Contains organizationUuid, accountUuid required for OAuth
	"userID",        // User identifier for session tracking
	"anonymousId",   // Session ID - required for x-organization-uuid header to be sent
	"installMethod", // Installation method - affects OAuth header behavior

	// Version and usage tracking (affects API client behavior)
	"lastOnboardingVersion", // Last completed onboarding version
	"lastReleaseNotesSeen",  // Last seen release notes
	"numStartups",           // Startup count for metrics

	// Feature flags and migrations
	"sonnet45MigrationComplete",
	"opus45MigrationComplete",
	"opusProMigrationComplete",
	"thinkingMigrationComplete",

	// Client configuration cache (server-provided settings)
	"clientDataCache", // Contains system_prompt_variant and other runtime config

	// Performance optimizations (optional)
	"cachedGrowthBookFeatures", // Feature flag cache - reduces startup API calls

	// First launch tracking
	"firstStartTime", // Initial startup timestamp
}

// ReadHostConfig reads the host's ~/.claude.json and returns allowlisted fields.
// Returns nil, nil if the file doesn't exist (same pattern as LoadSettings).
func ReadHostConfig(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var full map[string]any
	if err := json.Unmarshal(data, &full); err != nil {
		return nil, err
	}

	result := make(map[string]any)
	for _, key := range HostConfigAllowlist {
		if v, ok := full[key]; ok {
			result[key] = v
		}
	}
	return result, nil
}

// WriteClaudeConfig writes a minimal ~/.claude.json to the staging directory.
// This skips the onboarding flow, sets dark theme, and optionally configures MCP servers.
// mcpServers is a map of server names to their configurations.
// hostConfig contains allowlisted fields from the host's ~/.claude.json to merge in.
func WriteClaudeConfig(stagingDir string, mcpServers map[string]MCPServerForContainer, hostConfig map[string]any) error {
	config := make(map[string]any)

	// Start with host config fields (if any)
	for k, v := range hostConfig {
		config[k] = v
	}

	// Our explicit fields take precedence over anything from hostConfig
	config["hasCompletedOnboarding"] = true
	config["theme"] = "dark"

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

// WriteCredentialsFile writes a placeholder credentials file to the staging directory.
// This should only be called for OAuth tokens - API keys don't need credential files.
//
// SECURITY: The real OAuth token is NEVER written to the container filesystem.
// Authentication is handled by the TLS-intercepting proxy at the network layer.
func WriteCredentialsFile(cred *provider.Credential, stagingDir string) error {
	if !isOAuthToken(cred.Token) {
		// API keys don't need credential files
		return nil
	}

	// Write credentials file with a placeholder token.
	// The real token is NEVER written to the container - it's injected by
	// the proxy at the network layer. Claude Code needs this file to exist
	// with valid structure to function, but the actual authentication is
	// handled transparently by the TLS-intercepting proxy.
	creds := oauthCredentials{
		ClaudeAiOauth: &oauthToken{
			AccessToken: ProxyInjectedPlaceholder,
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

// oauthCredentials represents the OAuth credentials stored by Claude Code.
type oauthCredentials struct {
	ClaudeAiOauth *oauthToken `json:"claudeAiOauth,omitempty"`
}

// oauthToken represents an individual OAuth token from Claude Code.
type oauthToken struct {
	AccessToken      string   `json:"accessToken"`
	RefreshToken     string   `json:"refreshToken,omitempty"`
	ExpiresAt        int64    `json:"expiresAt"` // Unix timestamp in milliseconds
	Scopes           []string `json:"scopes"`
	SubscriptionType string   `json:"subscriptionType,omitempty"`
	RateLimitTier    string   `json:"rateLimitTier,omitempty"`
}
