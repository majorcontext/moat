// Package credential provides credential management for Moat.
package credential

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/container"
)

// GitHubSetup implements ProviderSetup for GitHub credentials.
type GitHubSetup struct{}

// Provider returns the provider identifier.
func (g *GitHubSetup) Provider() Provider {
	return ProviderGitHub
}

// ConfigureProxy sets up proxy headers for GitHub.
func (g *GitHubSetup) ConfigureProxy(p ProxyConfigurer, cred *Credential) {
	p.SetCredential("api.github.com", "Bearer "+cred.Token)
	p.SetCredential("github.com", "Bearer "+cred.Token)
}

// ContainerEnv returns environment variables for GitHub.
//
// GH_TOKEN: Used by gh CLI for API authentication. We set a format-valid placeholder
// (ghp_...) that passes gh CLI's local validation. The proxy intercepts HTTPS requests
// and injects the real token via Authorization headers.
//
// GIT_TERMINAL_PROMPT: Set to 0 to disable interactive credential prompts from git.
// Note that git does NOT use GH_TOKEN - it needs separate credential configuration
// (like gh as a credential helper). This setting prevents git from prompting users
// for credentials when accessing GitHub HTTPS remotes, which would fail anyway since
// the container doesn't have stored credentials. Git operations that need credentials
// will fail silently instead of blocking on a prompt.
func (g *GitHubSetup) ContainerEnv(cred *Credential) []string {
	return []string{
		"GH_TOKEN=" + GitHubTokenPlaceholder,
		"GIT_TERMINAL_PROMPT=0",
	}
}

// ContainerMounts returns mounts for GitHub.
// Copies user's gh CLI config (for aliases, preferences) if it exists.
// Authentication is handled via GH_TOKEN environment variable.
func (g *GitHubSetup) ContainerMounts(cred *Credential, containerHome string) ([]container.MountConfig, string, error) {
	// Check if user has gh config to copy
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, "", nil // No home dir, skip config copy
	}

	userGhConfig := filepath.Join(homeDir, ".config", "gh", "config.yml")
	if _, statErr := os.Stat(userGhConfig); os.IsNotExist(statErr) {
		return nil, "", nil // No config to copy
	}

	// Create temp directory for gh config with restrictive permissions
	tmpDir, err := os.MkdirTemp("", "moat-gh-config-*")
	if err != nil {
		return nil, "", fmt.Errorf("creating gh config dir: %w", err)
	}
	if chmodErr := os.Chmod(tmpDir, 0700); chmodErr != nil {
		os.RemoveAll(tmpDir)
		return nil, "", fmt.Errorf("setting permissions on gh config dir: %w", chmodErr)
	}

	// Use defer to ensure cleanup on any error after temp dir creation
	success := false
	defer func() {
		if !success {
			os.RemoveAll(tmpDir)
		}
	}()

	// Create gh subdirectory (inherits restrictive permissions from parent)
	ghDir := filepath.Join(tmpDir, "gh")
	if mkdirErr := os.MkdirAll(ghDir, 0700); mkdirErr != nil {
		return nil, "", fmt.Errorf("creating gh dir: %w", mkdirErr)
	}

	// Copy user's config.yml (contains aliases, preferences, etc.)
	configContent, err := os.ReadFile(userGhConfig)
	if err != nil {
		return nil, "", fmt.Errorf("reading user gh config: %w", err)
	}

	configPath := filepath.Join(ghDir, "config.yml")
	if writeErr := os.WriteFile(configPath, configContent, 0600); writeErr != nil {
		return nil, "", fmt.Errorf("writing config.yml: %w", writeErr)
	}

	// Note: We intentionally do NOT copy hosts.yml - authentication is
	// handled via GH_TOKEN environment variable and proxy injection.

	// Mount the gh config directory into the container
	mounts := []container.MountConfig{
		{
			Source:   ghDir,
			Target:   filepath.Join(containerHome, ".config", "gh"),
			ReadOnly: false, // gh may want to update config
		},
	}

	success = true // Disable cleanup, caller takes ownership
	return mounts, tmpDir, nil
}

// Cleanup cleans up GitHub resources.
func (g *GitHubSetup) Cleanup(cleanupPath string) {
	if cleanupPath != "" {
		os.RemoveAll(cleanupPath)
	}
}

// GitHubImpliedDeps returns the dependencies implied by a GitHub grant.
func GitHubImpliedDeps() []string {
	return []string{"gh", "git"}
}
