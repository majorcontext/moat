package copilot

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

const (
	SourceCLI = "cli"
	SourceEnv = "env"
	SourcePAT = "pat"
)

// Provider implements provider.CredentialProvider and provider.AgentProvider
// for GitHub Copilot CLI.
type Provider struct{}

var (
	_ provider.CredentialProvider  = (*Provider)(nil)
	_ provider.AgentProvider       = (*Provider)(nil)
	_ provider.RefreshableProvider = (*Provider)(nil)
)

func init() {
	provider.Register(&Provider{})
}

// Name returns the provider identifier.
func (p *Provider) Name() string { return copilotProviderName }

// Grant acquires a Copilot-capable GitHub credential.
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	g := NewGrant()
	return g.Execute(ctx)
}

// ConfigureProxy sets up GitHub/Copilot credential injection.
func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	setProxyAuth(proxy, cred.Token)
}

func setProxyAuth(proxy provider.ProxyConfigurer, token string) {
	// copilotProxyHost and copilotTelemetry are excluded: they use session
	// tokens obtained via the Copilot token exchange (through api.github.com,
	// which does get injection), not the original PAT.
	proxy.SetCredentialWithGrant(copilotAPIHost, "Authorization", "Bearer "+token, copilotProviderName)
	proxy.SetCredentialWithGrant(copilotChatAPIHost, "Authorization", "Bearer "+token, copilotProviderName)
	proxy.SetCredentialWithGrant(copilotBusinessHost, "Authorization", "Bearer "+token, copilotProviderName)
	proxy.SetCredentialWithGrant(copilotMCPHost, "Authorization", "Bearer "+token, copilotProviderName)
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	proxy.SetCredentialWithGrant(copilotGitHost, "Authorization", "Basic "+basic, copilotProviderName)
}

// ContainerEnv returns Copilot auth placeholders. Copilot CLI checks
// COPILOT_GITHUB_TOKEN before GH_TOKEN/GITHUB_TOKEN; GH_TOKEN lets gh CLI use
// the same proxy-injected credential for GitHub operations inside the run.
func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	return []string{
		"COPILOT_GITHUB_TOKEN=" + credential.CopilotTokenPlaceholder,
		"GH_TOKEN=" + credential.CopilotTokenPlaceholder,
		"GIT_TERMINAL_PROMPT=0",
	}
}

// ContainerMounts returns none — Copilot uses the staging-directory approach.
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

// Cleanup is a no-op — staging-directory cleanup is handled by PrepareContainer.
func (p *Provider) Cleanup(cleanupPath string) {}

// ImpliedDependencies returns dependencies useful for Copilot's GitHub workflow.
func (p *Provider) ImpliedDependencies() []string { return []string{"gh", "git"} }

func (p *Provider) CanRefresh(cred *provider.Credential) bool {
	return cred != nil && cred.Metadata != nil && cred.Metadata[provider.MetaKeyTokenSource] == SourceCLI
}

func (p *Provider) RefreshInterval() time.Duration { return 30 * time.Minute }

func (p *Provider) Refresh(ctx context.Context, proxy provider.ProxyConfigurer, cred *provider.Credential) (*provider.Credential, error) {
	if !p.CanRefresh(cred) {
		return nil, provider.ErrRefreshNotSupported
	}
	token, err := getGHCLIToken(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateCopilotToken(ctx, token); err != nil {
		return nil, err
	}
	setProxyAuth(proxy, token)
	updated := *cred
	updated.Token = token
	return &updated, nil
}
