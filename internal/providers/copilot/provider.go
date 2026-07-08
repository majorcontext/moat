package copilot

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

// Provider implements provider.CredentialProvider and provider.AgentProvider
// for GitHub Copilot CLI.
type Provider struct{}

var (
	_ provider.CredentialProvider = (*Provider)(nil)
	_ provider.AgentProvider      = (*Provider)(nil)
)

func init() {
	provider.Register(&Provider{})
}

// Name returns the provider identifier.
func (p *Provider) Name() string { return copilotProviderName }

// Grant intentionally does not acquire a separate credential. Copilot CLI uses
// the github grant so there is one GitHub token to rotate and audit.
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	return nil, fmt.Errorf("GitHub Copilot CLI uses GitHub credentials.\n\nRun: moat grant github")
}

// ConfigureProxy sets up Copilot credential injection using a GitHub token.
func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	setProxyAuth(proxy, cred.Token)
}

func setProxyAuth(proxy provider.ProxyConfigurer, token string) {
	// copilotProxyHost and copilotTelemetry are excluded: they use session
	// tokens obtained via the Copilot token exchange (through api.github.com,
	// which does get injection), not the original PAT.
	proxy.SetCredentialWithGrant(copilotAPIHost, "Authorization", "Bearer "+token, "github")
	proxy.SetCredentialWithGrant(copilotChatAPIHost, "Authorization", "Bearer "+token, "github")
	proxy.SetCredentialWithGrant(copilotBusinessHost, "Authorization", "Bearer "+token, "github")
	proxy.SetCredentialWithGrant(copilotMCPHost, "Authorization", "Bearer "+token, "github")
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	proxy.SetCredentialWithGrant(copilotGitHost, "Authorization", "Basic "+basic, "github")
}

// ContainerEnv returns Copilot auth placeholders. The github grant supplies
// GH_TOKEN and git prompt behavior; Copilot only needs its preferred env var.
func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	return []string{
		"COPILOT_GITHUB_TOKEN=" + credential.CopilotTokenPlaceholder,
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
