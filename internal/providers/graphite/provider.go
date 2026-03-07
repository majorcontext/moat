package graphite

import (
	"path/filepath"

	"github.com/majorcontext/moat/internal/provider"
)

// placeholderToken is written into the container's Graphite config.
// The proxy replaces it with the real token on outgoing requests.
const placeholderToken = "moat-proxy-injected"

// Provider implements provider.CredentialProvider for Graphite.
type Provider struct{}

// Verify interface compliance at compile time.
var (
	_ provider.CredentialProvider = (*Provider)(nil)
	_ provider.InitFileProvider   = (*Provider)(nil)
)

func init() {
	provider.Register(&Provider{})
}

// Name returns the provider identifier.
func (p *Provider) Name() string {
	return "graphite"
}

// ConfigureProxy sets up proxy headers for Graphite API requests.
// The Graphite CLI uses "Authorization: token <token>" (not Bearer).
func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	proxy.SetCredentialWithGrant("api.graphite.com", "Authorization", "token "+cred.Token, "graphite")
}

// ContainerEnv returns environment variables for the container.
func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	return nil
}

// ContainerInitFiles returns config files to write into the container at startup.
// The Graphite CLI reads its auth token from ~/.config/graphite/user_config.
func (p *Provider) ContainerInitFiles(cred *provider.Credential, containerHome string) map[string]string {
	configPath := filepath.Join(containerHome, ".config", "graphite", "user_config")
	return map[string]string{
		configPath: `{"authToken":"` + placeholderToken + `"}`,
	}
}

// ContainerMounts returns no mounts — config is written via moat-init.sh.
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

// Cleanup is a no-op — no temp files are created.
func (p *Provider) Cleanup(cleanupPath string) {}

// ImpliedDependencies returns dependencies implied by this provider.
func (p *Provider) ImpliedDependencies() []string {
	return []string{"graphite-cli", "node", "git"}
}
