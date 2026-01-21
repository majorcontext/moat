package claude

import (
	"encoding/json"
	"fmt"
	"io"
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

// PopulateStagingDir populates the Claude staging directory with files needed for
// Claude Code to run properly. The staging directory will be copied to the container's
// home directory at startup by the moat-init script.
//
// Files added:
// - .credentials.json (placeholder token - real auth is via proxy)
// - .claude.json (copied from host, if exists)
// - statsig/ (copied from host, if exists)
// - stats-cache.json (copied from host, if exists)
//
// SECURITY: The real OAuth token is NEVER written to the container filesystem.
// Authentication is handled by the TLS-intercepting proxy at the network layer.
func (a *AnthropicSetup) PopulateStagingDir(cred *credential.Credential, stagingDir string) error {
	if !credential.IsOAuthToken(cred.Token) {
		// API keys don't need special files
		return nil
	}

	// Write credentials file with a placeholder token.
	// The real token is NEVER written to the container - it's injected by
	// the proxy at the network layer. Claude Code needs this file to exist
	// with valid structure to function, but the actual authentication is
	// handled transparently by the TLS-intercepting proxy.
	creds := credential.ClaudeOAuthCredentials{
		ClaudeAiOauth: &credential.ClaudeOAuthToken{
			AccessToken: ProxyInjectedPlaceholder,
			ExpiresAt:   cred.ExpiresAt.UnixMilli(),
			Scopes:      cred.Scopes,
		},
	}

	credsJSON, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("marshaling credentials: %w", err)
	}

	if err := os.WriteFile(filepath.Join(stagingDir, ".credentials.json"), credsJSON, 0600); err != nil {
		return fmt.Errorf("writing credentials file: %w", err)
	}

	// Copy files from host's ~/.claude directory
	hostHome, err := os.UserHomeDir()
	if err != nil {
		return nil // Non-fatal, just skip host files
	}

	hostClaudeDir := filepath.Join(hostHome, ".claude")

	// Copy ~/.claude.json (onboarding state and user preferences)
	hostClaudeJSON := filepath.Join(hostHome, ".claude.json")
	if _, err := os.Stat(hostClaudeJSON); err == nil {
		if err := copyFile(hostClaudeJSON, filepath.Join(stagingDir, ".claude.json")); err != nil {
			// Non-fatal, just log and continue
			fmt.Fprintf(os.Stderr, "Warning: failed to copy .claude.json: %v\n", err)
		}
	}

	// Copy statsig directory (feature flags)
	hostStatsig := filepath.Join(hostClaudeDir, "statsig")
	if info, err := os.Stat(hostStatsig); err == nil && info.IsDir() {
		if err := copyDir(hostStatsig, filepath.Join(stagingDir, "statsig")); err != nil {
			// Non-fatal, just log and continue
			fmt.Fprintf(os.Stderr, "Warning: failed to copy statsig directory: %v\n", err)
		}
	}

	// Copy stats-cache.json (usage stats, firstSessionDate)
	hostStatsCache := filepath.Join(hostClaudeDir, "stats-cache.json")
	if _, err := os.Stat(hostStatsCache); err == nil {
		if err := copyFile(hostStatsCache, filepath.Join(stagingDir, "stats-cache.json")); err != nil {
			// Non-fatal, just log and continue
			fmt.Fprintf(os.Stderr, "Warning: failed to copy stats-cache.json: %v\n", err)
		}
	}

	return nil
}

// copyFile copies a single file from src to dst.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	// Preserve permissions
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, srcInfo.Mode())
}

// copyDir recursively copies a directory from src to dst.
func copyDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

// init registers the Anthropic provider setup.
func init() {
	credential.RegisterProviderSetup(credential.ProviderAnthropic, &AnthropicSetup{})
}
