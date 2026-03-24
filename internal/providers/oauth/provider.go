package oauth

import (
	"context"
	"fmt"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

// Provider implements provider.CredentialProvider and provider.RefreshableProvider
// for OAuth-based credentials.
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
	return "oauth"
}

// Grant returns an error directing users to the proper CLI command.
// The real grant flow is handled by the CLI command `moat grant oauth <name>`.
func (p *Provider) Grant(_ context.Context) (*provider.Credential, error) {
	return nil, fmt.Errorf("use 'moat grant oauth <name>' instead")
}

// ConfigureProxy is a no-op. OAuth doesn't know which hosts to configure
// at registration time; host-specific proxy rules are set up per-grant.
func (p *Provider) ConfigureProxy(_ provider.ProxyConfigurer, _ *provider.Credential) {}

// ContainerEnv returns no environment variables.
func (p *Provider) ContainerEnv(_ *provider.Credential) []string {
	return nil
}

// ContainerMounts returns no mounts.
func (p *Provider) ContainerMounts(_ *provider.Credential, _ string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

// Cleanup is a no-op.
func (p *Provider) Cleanup(_ string) {}

// ImpliedDependencies returns no dependencies.
func (p *Provider) ImpliedDependencies() []string {
	return nil
}

// CanRefresh reports whether this credential supports background refresh.
// Requires metadata indicating an OAuth token source, a non-zero expiry, and a refresh token.
func (p *Provider) CanRefresh(cred *provider.Credential) bool {
	if cred.Metadata == nil {
		return false
	}
	if cred.Metadata[provider.MetaKeyTokenSource] != "oauth" {
		return false
	}
	if cred.ExpiresAt.IsZero() {
		return false
	}
	if cred.Metadata["refresh_token"] == "" {
		return false
	}
	return true
}

// RefreshInterval returns how often to attempt token refresh.
func (p *Provider) RefreshInterval() time.Duration {
	return 5 * time.Minute
}

// Refresh attempts to refresh the OAuth token using the refresh_token grant.
// Returns ErrRefreshNotSupported if the credential is nil or not refreshable.
func (p *Provider) Refresh(ctx context.Context, _ provider.ProxyConfigurer, cred *provider.Credential) (*provider.Credential, error) {
	if cred == nil || !p.CanRefresh(cred) {
		return nil, provider.ErrRefreshNotSupported
	}
	return refreshToken(ctx, cred)
}
