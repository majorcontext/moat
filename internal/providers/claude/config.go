package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/majorcontext/moat/internal/credential"
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
// Supports both HTTP relay servers (type: "http") and local process servers (type: "stdio").
type MCPServerForContainer struct {
	Type    string            `json:"type"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
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

	// Pre-accept the trust dialog for /workspace so Claude Code doesn't prompt
	// "Is this a project you trust?" on every container startup.
	config["projects"] = map[string]any{
		"/workspace": map[string]any{
			"hasTrustDialogAccepted": true,
		},
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

// WriteCredentialsFile writes a placeholder credentials file to the staging directory.
// This should only be called for OAuth tokens - API keys don't need credential files.
//
// SECURITY: The real OAuth token is NEVER written to the container filesystem.
// Authentication is handled by the TLS-intercepting proxy at the network layer.
func WriteCredentialsFile(cred *provider.Credential, stagingDir, subscriptionType, rateLimitTier string) error {
	if cred.Provider != "claude" {
		// Only OAuth credentials (provider "claude") need credential files.
		// API key credentials (provider "anthropic") skip this.
		return nil
	}

	// Write credentials file with a placeholder token.
	// The real token is NEVER written to the container - it's injected by
	// the proxy at the network layer. Claude Code needs this file to exist
	// with valid structure to function, but the actual authentication is
	// handled transparently by the TLS-intercepting proxy.
	//
	// The placeholder uses the sk-ant-oat01-* prefix so Claude Code recognizes
	// the session as OAuth-authenticated and takes the OAuth code path that
	// determines account capabilities.
	//
	// ExpiresAt handling: Setup-token grants are long-lived and don't carry
	// an expiry, so cred.ExpiresAt is the zero time.Time. UnixMilli() on the
	// zero value returns -62135596800000 (year 0001), which Claude Code reads
	// as an expired credential — the status line shows "not logged in" and
	// "API Usage Billing". Substitute a far-future expiry in that case.
	expiresAtMs := cred.ExpiresAt.UnixMilli()
	if cred.ExpiresAt.IsZero() {
		expiresAtMs = time.Now().Add(365 * 24 * time.Hour).UnixMilli()
	}

	// Scopes, subscriptionType, and rateLimitTier: Claude Code treats a session
	// with null scopes or no subscriptionType as unauthenticated ("API Usage
	// Billing"). Setup-token/pasted grants carry none of these, so resolve each:
	//   subscriptionType / rateLimitTier: moat.yaml override → value captured at
	//     grant time (imported creds, via metadata) → default.
	//   scopes: the grant's real scopes → default set.
	scopes := cred.Scopes
	if len(scopes) == 0 {
		// Copy, don't alias: defaultClaudeScopes is a package-level var and must
		// not be exposed for in-place mutation by future callers.
		scopes = append([]string(nil), defaultClaudeScopes...)
	}
	subType := firstNonEmpty(subscriptionType, cred.Metadata[MetaSubscriptionType], defaultSubscriptionType)
	rateTier := firstNonEmpty(rateLimitTier, cred.Metadata[MetaRateLimitTier])

	creds := oauthCredentials{
		ClaudeAiOauth: &oauthToken{
			AccessToken:      credential.ClaudeOAuthPlaceholder,
			ExpiresAt:        expiresAtMs,
			Scopes:           scopes,
			SubscriptionType: subType,
			RateLimitTier:    rateTier,
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

// Credential metadata keys used to thread subscription details captured at
// grant time (e.g. from imported host credentials) through to the container's
// .credentials.json. Setup-token and pasted-token grants don't carry these.
const (
	MetaSubscriptionType = "claude_subscription_type"
	MetaRateLimitTier    = "claude_rate_limit_tier"
)

// defaultSubscriptionType is written when a grant carries no subscription type
// and moat.yaml sets no override. Claude Code requires a non-empty
// subscriptionType to treat the session as a subscription rather than showing
// "API Usage Billing". The real plan is enforced server-side via the token the
// proxy injects, so this only affects what Claude Code displays/gates locally.
const defaultSubscriptionType = "max"

// defaultClaudeScopes is written when a grant carries no scopes (setup-token /
// pasted-token grants). These are the standard Claude Code OAuth scopes; an
// empty/null scopes array makes Claude Code treat the session as unauthenticated.
var defaultClaudeScopes = []string{
	"user:file_upload",
	"user:inference",
	"user:mcp_servers",
	"user:profile",
	"user:sessions:claude_code",
}

// subscriptionMetadata builds the credential metadata that carries subscription
// details captured at grant time through to WriteCredentialsFile. Returns nil
// when neither value is set (setup-token/pasted grants), so callers leave
// Metadata unset and the defaults/override apply.
func subscriptionMetadata(subscriptionType, rateLimitTier string) map[string]string {
	if subscriptionType == "" && rateLimitTier == "" {
		return nil
	}
	m := map[string]string{}
	if subscriptionType != "" {
		m[MetaSubscriptionType] = subscriptionType
	}
	if rateLimitTier != "" {
		m[MetaRateLimitTier] = rateLimitTier
	}
	return m
}

// firstNonEmpty returns the first non-empty string in vals, or "" if none.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
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
