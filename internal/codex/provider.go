// Package codex provides integration with the OpenAI Codex CLI agent.
// It handles credential setup, configuration file generation, and MCP
// (Model Context Protocol) configuration for running Codex in containers.
package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/andybons/moat/internal/container"
	"github.com/andybons/moat/internal/credential"
)

// CodexInitMountPath is where the staging directory is mounted in containers.
const CodexInitMountPath = "/moat/codex-init"

// OpenAISetup implements credential.ProviderSetup for OpenAI credentials.
// It handles both API keys and ChatGPT subscription tokens.
type OpenAISetup struct{}

// Provider returns the provider identifier.
func (o *OpenAISetup) Provider() credential.Provider {
	return credential.ProviderOpenAI
}

// ConfigureProxy sets up proxy headers for OpenAI API.
func (o *OpenAISetup) ConfigureProxy(p credential.ProxyConfigurer, cred *credential.Credential) {
	// OpenAI uses Bearer token authentication
	// api.openai.com is the main API endpoint
	// chatgpt.com is used for subscription token validation
	p.SetCredential("api.openai.com", "Bearer "+cred.Token)
	p.SetCredential("chatgpt.com", "Bearer "+cred.Token)
}

// ContainerEnv returns environment variables for OpenAI.
func (o *OpenAISetup) ContainerEnv(cred *credential.Credential) []string {
	// Set OPENAI_API_KEY with a placeholder that looks like a valid API key.
	// This tells Codex CLI it's authenticated (skips login prompts) and
	// bypasses local format validation.
	// The real token is injected by the proxy at the network layer.
	return []string{"OPENAI_API_KEY=" + credential.OpenAIAPIKeyPlaceholder}
}

// ContainerMounts returns mounts needed for OpenAI/Codex.
// This method returns empty because Codex setup uses the staging directory
// approach instead of direct mounts. The staging directory is populated by
// PopulateStagingDir and copied to the container at startup by moat-init.
func (o *OpenAISetup) ContainerMounts(cred *credential.Credential, containerHome string) ([]container.MountConfig, string, error) {
	// No direct mounts - we use the staging directory approach instead
	return nil, "", nil
}

// Cleanup cleans up OpenAI resources.
func (o *OpenAISetup) Cleanup(cleanupPath string) {
	// Nothing to clean up - staging directory is handled by the caller
}

// PopulateStagingDir populates the Codex staging directory with auth configuration.
//
// Files added:
// - auth.json (placeholder credentials - real auth is via proxy)
//
// SECURITY: The real token is NEVER written to the container filesystem.
// Authentication is handled by the TLS-intercepting proxy at the network layer.
func (o *OpenAISetup) PopulateStagingDir(cred *credential.Credential, stagingDir string) error {
	var authFile credential.CodexAuthFile

	if credential.IsCodexToken(cred.Token) {
		// ChatGPT subscription token - use the token structure
		// If no expiration is set, use a far-future time to prevent local expiration checks
		expiresAt := cred.ExpiresAt.Unix()
		if cred.ExpiresAt.IsZero() || expiresAt < 0 {
			// Set to 1 year from now - Codex CLI checks expiration locally
			expiresAt = time.Now().Add(365 * 24 * time.Hour).Unix()
		}
		authFile.Token = &credential.CodexAuthToken{
			AccessToken: credential.ProxyInjectedPlaceholder,
			ExpiresAt:   expiresAt,
		}
	} else {
		// API key - use a placeholder that looks like a valid API key
		// This bypasses local format validation in Codex CLI.
		// The proxy will inject the real key in the Authorization header.
		authFile.APIKey = credential.OpenAIAPIKeyPlaceholder
	}

	authJSON, err := json.MarshalIndent(authFile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling auth file: %w", err)
	}

	if writeErr := os.WriteFile(filepath.Join(stagingDir, "auth.json"), authJSON, 0600); writeErr != nil {
		return fmt.Errorf("writing auth file: %w", writeErr)
	}

	return nil
}

// WriteCodexConfig writes a minimal ~/.codex/config.toml to the staging directory.
// This provides default settings for the Codex CLI.
func WriteCodexConfig(stagingDir string) error {
	// Minimal config to set up Codex with sensible defaults
	// Using TOML format as Codex expects
	config := `# Moat-generated Codex configuration
# Real authentication is handled by the Moat proxy

[shell_environment_policy]
inherit = "core"
`

	if err := os.WriteFile(filepath.Join(stagingDir, "config.toml"), []byte(config), 0600); err != nil {
		return fmt.Errorf("writing config.toml: %w", err)
	}

	return nil
}

// init registers the OpenAI provider setup.
func init() {
	credential.RegisterProviderSetup(credential.ProviderOpenAI, &OpenAISetup{})
}
