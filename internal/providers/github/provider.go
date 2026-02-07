package github

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

// Token source values stored in Credential.Metadata[provider.MetaKeyTokenSource].
const (
	SourceCLI = "cli" // From `gh auth token` - refreshable
	SourceEnv = "env" // From GITHUB_TOKEN/GH_TOKEN env var - refreshable
	SourcePAT = "pat" // Interactive PAT entry - static
)

// Provider implements provider.CredentialProvider for GitHub.
type Provider struct{}

// Verify interface compliance at compile time.
var (
	_ provider.CredentialProvider  = (*Provider)(nil)
	_ provider.RefreshableProvider = (*Provider)(nil)
)

func init() {
	provider.Register(&Provider{})
}

// Name returns the provider identifier.
func (p *Provider) Name() string {
	return "github"
}

// ConfigureProxy sets up proxy headers for GitHub.
func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	proxy.SetCredentialWithGrant("api.github.com", "Authorization", "Bearer "+cred.Token, "github")
	proxy.SetCredentialWithGrant("github.com", "Authorization", "Bearer "+cred.Token, "github")
}

// ContainerEnv returns environment variables for GitHub.
//
// GH_TOKEN: Used by gh CLI for API authentication. We set a format-valid placeholder
// (ghp_...) that passes gh CLI's local validation. The proxy intercepts HTTPS requests
// and injects the real token via Authorization headers.
//
// GIT_TERMINAL_PROMPT: Set to 0 to disable interactive credential prompts from git.
func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	return []string{
		"GH_TOKEN=" + credential.GitHubTokenPlaceholder,
		"GIT_TERMINAL_PROMPT=0",
	}
}

// ContainerMounts returns mounts for GitHub.
// Copies user's gh CLI config (for aliases, preferences) if it exists.
// Authentication is handled via GH_TOKEN environment variable.
// Returns the temp directory path for cleanup when the run ends.
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
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

	mounts := []provider.MountConfig{
		{
			Source:   ghDir,
			Target:   filepath.Join(containerHome, ".config", "gh"),
			ReadOnly: false,
		},
	}

	success = true
	return mounts, tmpDir, nil
}

// CanRefresh reports whether this credential can be refreshed.
// Returns false for static credentials (PATs) and legacy credentials without metadata.
func (p *Provider) CanRefresh(cred *provider.Credential) bool {
	if cred.Metadata == nil {
		return false
	}
	source := cred.Metadata[provider.MetaKeyTokenSource]
	return source == SourceCLI || source == SourceEnv
}

// RefreshInterval returns how often to attempt refresh.
func (p *Provider) RefreshInterval() time.Duration {
	return 30 * time.Minute
}

// Cleanup cleans up GitHub resources.
func (p *Provider) Cleanup(cleanupPath string) {
	if cleanupPath != "" {
		os.RemoveAll(cleanupPath)
	}
}

// ImpliedDependencies returns dependencies implied by this provider.
func (p *Provider) ImpliedDependencies() []string {
	return []string{"gh", "git"}
}
