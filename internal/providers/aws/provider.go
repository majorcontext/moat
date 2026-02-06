package aws

import (
	"context"
	"net/http"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

// Provider implements provider.CredentialProvider and provider.EndpointProvider
// for AWS credentials via STS AssumeRole.
type Provider struct{}

// Compile-time interface assertions.
var (
	_ provider.CredentialProvider = (*Provider)(nil)
	_ provider.EndpointProvider   = (*Provider)(nil)
)

// New creates a new AWS provider.
func New() *Provider {
	return &Provider{}
}

func init() {
	provider.Register(New())
}

// Name returns the provider identifier.
func (p *Provider) Name() string {
	return "aws"
}

// Grant acquires AWS credentials by prompting for an IAM role ARN.
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	return grant(ctx)
}

// ConfigureProxy is a no-op for AWS since it uses the endpoint pattern.
// AWS credentials are served via RegisterEndpoints, not header injection.
func (p *Provider) ConfigureProxy(pc provider.ProxyConfigurer, cred *provider.Credential) {
	// No-op: AWS uses credential endpoint, not proxy header injection
}

// ContainerEnv returns nil; the run manager sets AWS_CONTAINER_CREDENTIALS_FULL_URI.
func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	// The run manager configures AWS_CONTAINER_CREDENTIALS_FULL_URI
	// pointing to the proxy's credential endpoint.
	return nil
}

// ContainerMounts returns nil; AWS doesn't require any mounts.
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

// CanRefresh returns false; credentials are fetched at runtime via the endpoint.
func (p *Provider) CanRefresh(cred *provider.Credential) bool {
	return false
}

// RefreshInterval returns 0; AWS doesn't use periodic refresh.
func (p *Provider) RefreshInterval() time.Duration {
	return 0
}

// Refresh is not supported for AWS; credentials are fetched on demand.
func (p *Provider) Refresh(ctx context.Context, pc provider.ProxyConfigurer, cred *provider.Credential) (*provider.Credential, error) {
	return nil, provider.ErrRefreshNotSupported
}

// Cleanup is a no-op for AWS.
func (p *Provider) Cleanup(cleanupPath string) {
	// No cleanup needed
}

// ImpliedDependencies returns dependencies implied by AWS grant.
func (p *Provider) ImpliedDependencies() []string {
	return []string{"aws"}
}

// RegisterEndpoints registers the AWS credential endpoint handler.
// The handler serves temporary credentials from STS AssumeRole.
func (p *Provider) RegisterEndpoints(mux *http.ServeMux, cred *provider.Credential) {
	handler := NewEndpointHandler(cred)
	mux.Handle("/aws-credentials", handler)
}
