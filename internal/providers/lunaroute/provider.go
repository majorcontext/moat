package lunaroute

import (
	"path/filepath"

	"github.com/majorcontext/moat/internal/provider"
)

const (
	// GatewayHost serves inference and the model catalog.
	GatewayHost = "gw.lunaroute.com"

	// MCPHost serves the tools the Pi extension registers.
	MCPHost = "mcp.lunaroute.com"

	// PlaceholderKey is written into the container's Pi auth.json. The proxy
	// replaces the header carrying it with the real key. It must stay a plain
	// literal: Pi resolves auth.json keys that look like env-var names or
	// start with "!" as references rather than values.
	PlaceholderKey = "moat-proxy-injected"
)

// Provider implements provider.CredentialProvider for LunaRoute.
type Provider struct{}

var (
	_ provider.CredentialProvider = (*Provider)(nil)
	_ provider.InitFileProvider   = (*Provider)(nil)
)

func init() {
	provider.Register(&Provider{})
}

// Name returns the provider identifier.
func (p *Provider) Name() string { return "lunaroute" }

// ConfigureProxy injects the key on LunaRoute's inference and MCP hosts, in
// the header format each expects. Nothing is registered for any other host.
func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	proxy.SetCredentialWithGrant(GatewayHost, "Authorization", "Bearer "+cred.Token, "lunaroute")
	proxy.SetCredentialWithGrant(MCPHost, "LUNAROUTE-API-KEY", cred.Token, "lunaroute")
}

// ContainerEnv returns nothing: Pi has no LunaRoute env var and reads the key
// from auth.json (see ContainerInitFiles).
func (p *Provider) ContainerEnv(cred *provider.Credential) []string { return nil }

// ContainerInitFiles writes a Pi auth.json holding a placeholder key, so the
// LunaRoute extension considers itself signed in and sends requests the proxy
// can inject into. Harmless in runs that don't use Pi.
//
// The entry must be an "oauth" credential, the shape LunaRoute's own login and
// `lunaroute setup pi --key` write: the extension registers an OAuth provider,
// and Pi does not resolve an "api_key" entry for it (measured on Pi 0.87.1 —
// "No API key found"). expires is far in the future so Pi never tries to
// refresh a login that has no refresh token.
func (p *Provider) ContainerInitFiles(cred *provider.Credential, containerHome string) map[string]string {
	path := filepath.Join(containerHome, ".pi", "agent", "auth.json")
	return map[string]string{
		path: `{"lunaroute":{"type":"oauth","access":"` + PlaceholderKey + `","refresh":"","expires":` + placeholderExpiresMs + `}}`,
	}
}

// placeholderExpiresMs is 2100-01-01T00:00:00Z in Unix milliseconds.
const placeholderExpiresMs = "4102444800000"

// ContainerMounts returns no mounts — auth.json is written via moat-init.
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

// Cleanup is a no-op — no temp files are created.
func (p *Provider) Cleanup(cleanupPath string) {}

// ImpliedDependencies returns none; Pi's dependencies come from `moat pi`.
func (p *Provider) ImpliedDependencies() []string { return nil }
