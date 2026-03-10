package meta

import (
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

// Metadata keys for app credentials used in token refresh.
const (
	MetaKeyAppID     = "meta_app_id"
	MetaKeyAppSecret = "meta_app_secret"
)

// Provider implements provider.CredentialProvider for Meta Graph API.
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
	return "meta"
}

// ConfigureProxy sets up proxy headers for Meta Graph API.
func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	proxy.SetCredentialWithGrant("graph.facebook.com", "Authorization", "Bearer "+cred.Token, "meta")
	proxy.SetCredentialWithGrant("graph.instagram.com", "Authorization", "Bearer "+cred.Token, "meta")
}

// ContainerEnv returns environment variables for Meta. None needed.
func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	return nil
}

// ContainerMounts returns mounts for Meta. None needed.
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

// Cleanup cleans up Meta resources. Nothing to do.
func (p *Provider) Cleanup(cleanupPath string) {}

// ImpliedDependencies returns dependencies implied by this provider. None.
func (p *Provider) ImpliedDependencies() []string {
	return nil
}

// CanRefresh reports whether this credential can be refreshed.
// Requires both app ID and app secret in metadata.
func (p *Provider) CanRefresh(cred *provider.Credential) bool {
	if cred.Metadata == nil {
		return false
	}
	return cred.Metadata[MetaKeyAppID] != "" && cred.Metadata[MetaKeyAppSecret] != ""
}

// RefreshInterval returns how often to attempt refresh.
func (p *Provider) RefreshInterval() time.Duration {
	return 24 * time.Hour
}
