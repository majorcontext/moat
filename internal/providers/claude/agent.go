package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
)

// PrepareContainer sets up staging directories and config files for Claude Code.
// It creates the necessary files that will be copied into the container at startup.
//
// If opts.HostConfig is nil, this method reads the host's ~/.claude.json automatically.
//
// This method works with both OAuth tokens and API keys. The credential type
// determines which environment variable placeholder is set.
func (p *OAuthProvider) PrepareContainer(ctx context.Context, opts provider.PrepareOpts) (*provider.ContainerConfig, error) {
	// Create a temporary staging directory
	tmpDir, err := os.MkdirTemp("", "moat-claude-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp directory: %w", err)
	}

	// Ensure proper permissions
	if err := os.Chmod(tmpDir, 0700); err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("setting permissions on staging dir: %w", err)
	}

	success := false
	defer func() {
		if !success {
			os.RemoveAll(tmpDir)
		}
	}()

	// Write credentials file for OAuth tokens.
	// Enrich with subscription info from the host's Claude Code credentials
	// so Claude Code in the container knows the account tier (e.g. Teams/Max for 1M context).
	if opts.Credential != nil {
		enrichCredentialFromHost(opts.Credential)
		if err := WriteCredentialsFile(opts.Credential, tmpDir); err != nil {
			return nil, fmt.Errorf("writing credentials file: %w", err)
		}
	}

	// Convert MCP servers to Claude's format
	var mcpServers map[string]MCPServerForContainer
	if len(opts.MCPServers) > 0 || len(opts.LocalMCPServers) > 0 {
		mcpServers = make(map[string]MCPServerForContainer)
		// Remote/relay MCP servers (type: http)
		for name, cfg := range opts.MCPServers {
			mcpServers[name] = MCPServerForContainer{
				Type:    "http",
				URL:     cfg.URL,
				Headers: cfg.Headers,
			}
		}
		// Local process MCP servers (type: stdio)
		for name, cfg := range opts.LocalMCPServers {
			if _, exists := mcpServers[name]; exists {
				return nil, fmt.Errorf("mcp server name %q is used by both a remote and a local server — names must be unique", name)
			}
			mcpServers[name] = MCPServerForContainer{
				Type:    "stdio",
				Command: cfg.Command,
				Args:    cfg.Args,
				Env:     cfg.Env,
				Cwd:     cfg.Cwd,
			}
		}
	}

	// Resolve host home directory once for host config and remote-settings.
	hostHome, hostHomeErr := os.UserHomeDir()

	// Get host config - use provided or read from host's ~/.claude.json
	var hostConfig map[string]any
	if opts.HostConfig != nil {
		hostConfig = opts.HostConfig
	} else if hostHomeErr == nil {
		// Read host config automatically
		hostConfig, _ = ReadHostConfig(filepath.Join(hostHome, ".claude.json"))
		// Ignore errors - missing host config is OK
	}

	// Write .claude.json config
	if err := WriteClaudeConfig(tmpDir, mcpServers, hostConfig); err != nil {
		return nil, fmt.Errorf("writing claude config: %w", err)
	}

	// Copy remote-settings.json from host's ~/.claude/ directory.
	// This caches the server-managed settings so Claude Code doesn't prompt
	// for managed settings approval on every container startup.
	if hostHomeErr == nil {
		remoteSettingsPath := filepath.Join(hostHome, ".claude", "remote-settings.json")
		if data, readErr := os.ReadFile(remoteSettingsPath); readErr == nil {
			if writeErr := os.WriteFile(filepath.Join(tmpDir, "remote-settings.json"), data, 0600); writeErr != nil {
				log.Debug("failed to stage remote-settings.json", "error", writeErr)
			}
		}
	}

	// Write runtime context file if provided
	if opts.RuntimeContext != "" {
		if err := os.WriteFile(filepath.Join(tmpDir, "CLAUDE.md"), []byte(opts.RuntimeContext), 0644); err != nil {
			return nil, fmt.Errorf("writing context file: %w", err)
		}
	}

	// Build mounts
	mounts := []provider.MountConfig{
		{
			Source:   tmpDir,
			Target:   ClaudeInitMountPath,
			ReadOnly: true,
		},
	}

	// Build environment variables based on credential type.
	// PrepareContainer can be called with either OAuth or API key credentials.
	env := containerEnvForCredential(opts.Credential)
	env = append(env, "MOAT_CLAUDE_INIT="+ClaudeInitMountPath)

	success = true
	return &provider.ContainerConfig{
		Env:        env,
		Mounts:     mounts,
		StagingDir: tmpDir,
		Cleanup: func() {
			os.RemoveAll(tmpDir)
		},
	}, nil
}

// containerEnvForCredential returns the correct environment variable based on
// the credential's provider identity. API key credentials (provider "anthropic")
// get ANTHROPIC_API_KEY with a placeholder value — real credentials are injected
// by the proxy at the network layer. OAuth credentials (provider "claude") rely
// on .credentials.json instead of an environment variable.
func containerEnvForCredential(cred *provider.Credential) []string {
	if cred == nil {
		return nil
	}
	if cred.Provider == "claude" {
		// NOTE: We intentionally do NOT set CLAUDE_CODE_OAUTH_TOKEN here.
		// When that env var is set with a non-OAuth-looking placeholder,
		// Claude Code may not recognize the session as OAuth-authenticated,
		// preventing features like 1M context window from working.
		//
		// Instead, we write a .credentials.json with the OAuth placeholder
		// token (sk-ant-oat01-... prefix) and subscription metadata. Claude
		// Code reads this file when the env var is absent.
		return nil
	}
	return []string{"ANTHROPIC_API_KEY=" + ProxyInjectedPlaceholder}
}

// enrichCredentialFromHost populates subscription metadata and cached bootstrap
// on the credential if not already present. It tries two sources:
//
//  1. The credential's existing metadata (from grant-time caching — most reliable)
//  2. The host's ~/.claude/.credentials.json + live API call (fallback for older grants)
//
// Source 2 requires the host's short-lived OAuth token to be valid (~1hr lifetime).
// Source 1 is preferred because the bootstrap was cached at grant time when the
// host token was fresh.
func enrichCredentialFromHost(cred *provider.Credential) {
	if cred.Provider != "claude" {
		return
	}
	// Already has cached bootstrap from grant-time caching
	if cred.Metadata != nil && cred.Metadata[metaKeyCachedBootstrap] != "" {
		log.Debug("credential already has cached bootstrap",
			"subsystem", "claude",
			"bootstrap_len", len(cred.Metadata[metaKeyCachedBootstrap]),
		)
		return
	}

	cc := &credential.ClaudeCodeCredentials{}
	hostToken, err := cc.GetClaudeCodeCredentials()
	if err != nil {
		log.Debug("could not read host Claude credentials for bootstrap cache",
			"subsystem", "claude",
			"error", err,
		)
		return
	}

	if hostToken.AccessToken == "" || hostToken.IsExpired() {
		log.Debug("host Claude token unavailable or expired, skipping bootstrap cache",
			"subsystem", "claude",
			"has_token", hostToken.AccessToken != "",
			"expired", hostToken.IsExpired(),
		)
		return
	}

	if cred.Metadata == nil {
		cred.Metadata = make(map[string]string)
	}

	// Fetch subscription info from profile (for .credentials.json)
	if cred.Metadata["subscriptionType"] == "" {
		subType, rateTier := fetchProfileSubscription(hostToken.AccessToken)
		if subType != "" {
			cred.Metadata["subscriptionType"] = subType
			if rateTier != "" {
				cred.Metadata["rateLimitTier"] = rateTier
			}
		}
	}

	// Fetch full bootstrap response (for proxy to serve)
	bootstrap := fetchBootstrapResponse(hostToken.AccessToken)
	if bootstrap != "" {
		cred.Metadata[metaKeyCachedBootstrap] = bootstrap
		log.Debug("cached bootstrap response from host OAuth token",
			"subsystem", "claude",
			"bootstrap_len", len(bootstrap),
		)
	}
}

// fetchProfileSubscription calls /api/oauth/profile with the given OAuth token
// to retrieve subscription metadata. Returns empty strings on any failure.
func fetchProfileSubscription(accessToken string) (subscriptionType, rateLimitTier string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.anthropic.com/api/oauth/profile", nil)
	if err != nil {
		return "", ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Debug("failed to fetch OAuth profile for subscription info",
			"subsystem", "claude",
			"error", err,
		)
		return "", ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", ""
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return "", ""
	}

	var profile struct {
		SubscriptionType string `json:"subscriptionType"`
		RateLimitTier    string `json:"rateLimitTier"`
	}
	if err := json.Unmarshal(body, &profile); err != nil {
		return "", ""
	}

	return profile.SubscriptionType, profile.RateLimitTier
}

// fetchBootstrapResponse calls /api/bootstrap with the given OAuth token and
// returns the full response body as a string. Returns empty string on any failure.
func fetchBootstrapResponse(accessToken string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.anthropic.com/api/bootstrap", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Debug("failed to fetch bootstrap for cache",
			"subsystem", "claude",
			"error", err,
		)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Debug("bootstrap returned non-200",
			"subsystem", "claude",
			"status", resp.StatusCode,
		)
		return ""
	}

	// Bootstrap responses are typically ~50-60KB
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return ""
	}

	return string(body)
}
