package claude

import (
	"encoding/json"
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
		// OAuth token from Claude Code - use Bearer auth
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
		// For OAuth, don't set ANTHROPIC_API_KEY - Claude Code should use OAuth
		return nil
	}
	// For API keys, set a placeholder so Claude Code doesn't error
	// The real key is injected by the proxy at the network layer
	return []string{"ANTHROPIC_API_KEY=" + ProxyInjectedPlaceholder}
}

// ContainerMounts returns mounts needed for Anthropic/Claude Code.
func (a *AnthropicSetup) ContainerMounts(cred *credential.Credential, containerHome string) ([]container.MountConfig, string, error) {
	if !credential.IsOAuthToken(cred.Token) {
		// API keys don't need special mounts
		return nil, "", nil
	}

	// Create credentials directory for OAuth
	credsDir, err := a.createCredentialsDir(cred)
	if err != nil {
		return nil, "", err
	}

	var mounts []container.MountConfig
	containerClaudeDir := filepath.Join(containerHome, ".claude")

	// Mount our generated credentials file
	credsFile := filepath.Join(credsDir, ".credentials.json")
	mounts = append(mounts, container.MountConfig{
		Source:   credsFile,
		Target:   filepath.Join(containerClaudeDir, ".credentials.json"),
		ReadOnly: true,
	})

	// Mount host's ~/.claude.json for onboarding state and user preferences
	// This file contains hasCompletedOnboarding and other important state
	// Must be writable as Claude Code updates it during operation
	if hostHome, err := os.UserHomeDir(); err == nil {
		hostClaudeJSON := filepath.Join(hostHome, ".claude.json")
		if _, err := os.Stat(hostClaudeJSON); err == nil {
			mounts = append(mounts, container.MountConfig{
				Source:   hostClaudeJSON,
				Target:   filepath.Join(containerHome, ".claude.json"),
				ReadOnly: false,
			})
		}

		hostClaudeDir := filepath.Join(hostHome, ".claude")

		// Mount statsig directory for feature flags
		hostStatsig := filepath.Join(hostClaudeDir, "statsig")
		if info, err := os.Stat(hostStatsig); err == nil && info.IsDir() {
			mounts = append(mounts, container.MountConfig{
				Source:   hostStatsig,
				Target:   filepath.Join(containerClaudeDir, "statsig"),
				ReadOnly: true,
			})
		}

		// Mount stats-cache.json which contains firstSessionDate (onboarding state)
		hostStatsCache := filepath.Join(hostClaudeDir, "stats-cache.json")
		if _, err := os.Stat(hostStatsCache); err == nil {
			mounts = append(mounts, container.MountConfig{
				Source:   hostStatsCache,
				Target:   filepath.Join(containerClaudeDir, "stats-cache.json"),
				ReadOnly: true,
			})
		}
	}

	return mounts, credsDir, nil
}

// Cleanup cleans up Anthropic resources.
func (a *AnthropicSetup) Cleanup(cleanupPath string) {
	if cleanupPath != "" {
		_ = os.RemoveAll(cleanupPath)
	}
}

// createCredentialsDir creates a temp directory with a Claude credentials file.
// Returns the directory path (caller is responsible for cleanup).
func (a *AnthropicSetup) createCredentialsDir(cred *credential.Credential) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	baseDir := filepath.Join(home, ".moat", "claude-creds")
	if mkdirErr := os.MkdirAll(baseDir, 0700); mkdirErr != nil {
		return "", mkdirErr
	}

	// Create a unique subdirectory for this run
	credsDir, err := os.MkdirTemp(baseDir, "run-")
	if err != nil {
		return "", err
	}

	// Build credentials JSON matching Claude Code's format
	creds := credential.ClaudeOAuthCredentials{
		ClaudeAiOauth: &credential.ClaudeOAuthToken{
			AccessToken: cred.Token,
			ExpiresAt:   cred.ExpiresAt.UnixMilli(),
			Scopes:      cred.Scopes,
		},
	}

	credsJSON, err := json.Marshal(creds)
	if err != nil {
		os.RemoveAll(credsDir)
		return "", err
	}

	// Write credentials file
	credsFile := filepath.Join(credsDir, ".credentials.json")
	if err := os.WriteFile(credsFile, credsJSON, 0600); err != nil {
		os.RemoveAll(credsDir)
		return "", err
	}

	return credsDir, nil
}

// init registers the Anthropic provider setup.
func init() {
	credential.RegisterProviderSetup(credential.ProviderAnthropic, &AnthropicSetup{})
}
