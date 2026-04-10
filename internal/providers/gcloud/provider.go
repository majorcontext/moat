package gcloud

import (
	"context"
	"net/http"

	"github.com/majorcontext/moat/internal/provider"
)

// Provider implements provider.CredentialProvider and provider.EndpointProvider
// for Google Cloud credentials via GCE metadata server emulation.
type Provider struct{}

// Compile-time interface assertions.
var (
	_ provider.CredentialProvider = (*Provider)(nil)
	_ provider.EndpointProvider   = (*Provider)(nil)
)

// New creates a new gcloud provider.
func New() *Provider { return &Provider{} }

func init() { provider.Register(New()) }

// Name returns the provider identifier.
func (p *Provider) Name() string { return "gcloud" }

// Grant acquires Google Cloud credentials from the host environment.
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	return grant(ctx)
}

// ConfigureProxy is a no-op for gcloud since it uses the metadata endpoint.
func (p *Provider) ConfigureProxy(pc provider.ProxyConfigurer, cred *provider.Credential) {
	// No-op: gcloud uses metadata endpoint, not header injection.
}

// ContainerEnv returns nil; the run manager sets GCE_METADATA_HOST.
func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	// Env vars are injected by the run manager since they depend on the
	// per-run proxy host:port.
	return nil
}

// ContainerMounts returns nil; gcloud doesn't require any mounts.
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

// Cleanup is a no-op for gcloud.
func (p *Provider) Cleanup(cleanupPath string) {}

// ImpliedDependencies returns dependencies implied by gcloud grant.
func (p *Provider) ImpliedDependencies() []string { return []string{"gcloud"} }

// RegisterEndpoints registers HTTP handlers for the metadata emulation.
func (p *Provider) RegisterEndpoints(mux *http.ServeMux, cred *provider.Credential) {
	// Metadata emulation is served by a per-run handler attached to the
	// RunContext at daemon-register time, not via this package-level mux.
}
