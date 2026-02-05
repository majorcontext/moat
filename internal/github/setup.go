// Package github implements credential setup and refresh for GitHub tokens.
package github

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/credential"
)

// Token source values stored in Credential.Metadata[credential.MetaKeyTokenSource].
const (
	SourceCLI = "cli" // From `gh auth token` — refreshable
	SourceEnv = "env" // From GITHUB_TOKEN/GH_TOKEN env var — refreshable
	SourcePAT = "pat" // Interactive PAT entry — static
)

// Setup implements credential.ProviderSetup and credential.TokenRefresher
// for GitHub credentials.
type Setup struct{}

func init() {
	credential.RegisterProviderSetup(credential.ProviderGitHub, &Setup{})
	credential.RegisterImpliedDeps(credential.ProviderGitHub, func() []string {
		return []string{"gh", "git"}
	})
}

// Provider returns the provider identifier.
func (s *Setup) Provider() credential.Provider {
	return credential.ProviderGitHub
}

// ConfigureProxy sets up proxy headers for GitHub.
func (s *Setup) ConfigureProxy(p credential.ProxyConfigurer, cred *credential.Credential) {
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
func (s *Setup) ContainerEnv(cred *credential.Credential) []string {
	return []string{
		"GH_TOKEN=" + credential.GitHubTokenPlaceholder,
		"GIT_TERMINAL_PROMPT=0",
	}
}

// ContainerMounts returns mounts for GitHub.
// Copies user's gh CLI config (for aliases, preferences) if it exists.
// Authentication is handled via GH_TOKEN environment variable.
func (s *Setup) ContainerMounts(cred *credential.Credential, containerHome string) ([]container.MountConfig, string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, "", nil
	}

	userGhConfig := filepath.Join(homeDir, ".config", "gh", "config.yml")
	if _, statErr := os.Stat(userGhConfig); os.IsNotExist(statErr) {
		return nil, "", nil
	}

	tmpDir, err := os.MkdirTemp("", "moat-gh-config-*")
	if err != nil {
		return nil, "", fmt.Errorf("creating gh config dir: %w", err)
	}
	if chmodErr := os.Chmod(tmpDir, 0700); chmodErr != nil {
		os.RemoveAll(tmpDir)
		return nil, "", fmt.Errorf("setting permissions on gh config dir: %w", chmodErr)
	}

	success := false
	defer func() {
		if !success {
			os.RemoveAll(tmpDir)
		}
	}()

	ghDir := filepath.Join(tmpDir, "gh")
	if mkdirErr := os.MkdirAll(ghDir, 0700); mkdirErr != nil {
		return nil, "", fmt.Errorf("creating gh dir: %w", mkdirErr)
	}

	configContent, err := os.ReadFile(userGhConfig)
	if err != nil {
		return nil, "", fmt.Errorf("reading user gh config: %w", err)
	}

	configPath := filepath.Join(ghDir, "config.yml")
	if writeErr := os.WriteFile(configPath, configContent, 0600); writeErr != nil {
		return nil, "", fmt.Errorf("writing config.yml: %w", writeErr)
	}

	mounts := []container.MountConfig{
		{
			Source:   ghDir,
			Target:   filepath.Join(containerHome, ".config", "gh"),
			ReadOnly: false,
		},
	}

	success = true
	return mounts, tmpDir, nil
}

// Cleanup cleans up GitHub resources.
func (s *Setup) Cleanup(cleanupPath string) {
	if cleanupPath != "" {
		os.RemoveAll(cleanupPath)
	}
}

// CanRefresh reports whether this credential can be refreshed.
// Returns false for static credentials (PATs) and legacy credentials without metadata.
func (s *Setup) CanRefresh(cred *credential.Credential) bool {
	if cred.Metadata == nil {
		return false
	}
	source := cred.Metadata[credential.MetaKeyTokenSource]
	return source == SourceCLI || source == SourceEnv
}

// RefreshInterval returns how often to attempt refresh.
func (s *Setup) RefreshInterval() time.Duration {
	return 30 * time.Minute
}

// RefreshCredential re-acquires a fresh token from the original source
// and updates the proxy.
func (s *Setup) RefreshCredential(ctx context.Context, p credential.ProxyConfigurer, cred *credential.Credential) (*credential.Credential, error) {
	source := cred.Metadata[credential.MetaKeyTokenSource]
	var newToken string

	switch source {
	case SourceCLI:
		out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
		if err != nil {
			return nil, fmt.Errorf("gh auth token: %w", err)
		}
		newToken = strings.TrimSpace(string(out))
	case SourceEnv:
		newToken = os.Getenv("GITHUB_TOKEN")
		if newToken == "" {
			newToken = os.Getenv("GH_TOKEN")
		}
		if newToken == "" {
			return nil, fmt.Errorf("GITHUB_TOKEN and GH_TOKEN are both empty")
		}
	default:
		return nil, fmt.Errorf("source %q does not support refresh", source)
	}

	// Update proxy
	p.SetCredential("api.github.com", "Bearer "+newToken)
	p.SetCredential("github.com", "Bearer "+newToken)

	// Return updated credential
	updated := *cred
	updated.Token = newToken
	return &updated, nil
}
