package kiro

import (
	"context"

	"github.com/majorcontext/moat/internal/provider"
)

// Provider implements provider.CredentialProvider and provider.AgentProvider
// for the Kiro CLI.
type Provider struct{}

var (
	_ provider.CredentialProvider = (*Provider)(nil)
	_ provider.AgentProvider      = (*Provider)(nil)
)

func init() {
	provider.Register(&Provider{})
}

// Name returns the provider identifier.
func (p *Provider) Name() string { return "kiro" }

// Grant acquires a Kiro token interactively or from environment.
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	return NewGrant().Execute(ctx)
}

// ConfigureProxy injects the real Kiro Bearer token on the Kiro API hosts.
// Passthrough hosts receive no injection (they are only allowlisted).
func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	for _, host := range kiroAPIHosts {
		proxy.SetCredentialWithGrant(host, "Authorization", "Bearer "+cred.Token, "kiro")
	}
}

// ContainerEnv sets a placeholder KIRO_API_KEY. kiro-cli runs in API-key
// mode and sends the placeholder; the proxy swaps in the real token.
func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	return []string{"KIRO_API_KEY=" + KiroAPIKeyPlaceholder}
}

// ContainerMounts returns no direct mounts — Kiro uses the staging-dir
// approach populated by PrepareContainer.
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

// Cleanup is a no-op; the staging directory is cleaned by the caller.
func (p *Provider) Cleanup(cleanupPath string) {}

// ImpliedDependencies returns no implied dependencies.
func (p *Provider) ImpliedDependencies() []string { return nil }
