package github

import (
	"os"
	"path/filepath"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
	"gopkg.in/yaml.v3"
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
	_ provider.InitFileProvider    = (*Provider)(nil)
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

// ContainerInitFiles copies the user's gh CLI config (aliases, preferences)
// into the container via the init-file mechanism. This avoids a bind mount
// to ~/.config/gh which would cause Docker to create ~/.config as root,
// preventing the container user from writing to ~/.config.
//
// The config is sanitized to remove any embedded credentials (oauth_token,
// hosts section) that may be present when gh uses insecure storage.
// Authentication is handled separately via GH_TOKEN environment variable.
func (p *Provider) ContainerInitFiles(cred *provider.Credential, containerHome string) map[string]string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	userGhConfig := filepath.Join(homeDir, ".config", "gh", "config.yml")
	content, err := os.ReadFile(userGhConfig)
	if err != nil {
		return nil
	}

	sanitized, err := sanitizeGhConfig(content)
	if err != nil {
		log.Debug("sanitizing gh config", "error", err)
		return nil
	}
	if sanitized == nil {
		return nil
	}

	configPath := filepath.Join(containerHome, ".config", "gh", "config.yml")
	return map[string]string{
		configPath: string(sanitized),
	}
}

// sanitizeGhConfig removes credential-bearing fields from gh CLI config.
// Older gh versions (or --insecure-storage) store oauth_token in config.yml.
// We strip the hosts section entirely since authentication is handled by the
// proxy, and only pass through preferences (git_protocol, editor, aliases, etc.).
//
// Note: the YAML round-trip strips comments and may reorder keys.
func sanitizeGhConfig(content []byte) ([]byte, error) {
	var cfg map[string]interface{}
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return nil, err
	}
	delete(cfg, "hosts")
	delete(cfg, "oauth_token")
	// Remove nil values to prevent YAML null serialization.
	// When gh CLI encounters `http_unix_socket: null` in config.yml,
	// it interprets the YAML null as the literal string "null" and
	// tries to connect to a unix socket at that path. Stripping nil
	// values avoids this (see #234).
	for k, v := range cfg {
		if v == nil {
			delete(cfg, k)
		}
	}
	if len(cfg) == 0 {
		return nil, nil
	}
	return yaml.Marshal(cfg)
}

// ContainerMounts returns no mounts — config is written via moat-init.sh.
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
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

// Cleanup is a no-op — no temp files are created.
func (p *Provider) Cleanup(cleanupPath string) {}

// ImpliedDependencies returns dependencies implied by this provider.
func (p *Provider) ImpliedDependencies() []string {
	return []string{"gh", "git"}
}
