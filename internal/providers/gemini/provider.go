package gemini

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

// Provider implements provider.CredentialProvider and provider.AgentProvider
// for Google Gemini CLI credentials.
type Provider struct{}

// Ensure Provider implements the required interfaces.
var (
	_ provider.CredentialProvider  = (*Provider)(nil)
	_ provider.AgentProvider       = (*Provider)(nil)
	_ provider.RefreshableProvider = (*Provider)(nil)
)

func init() {
	provider.Register(&Provider{})
	// Register "google" as an alias so credentials stored under either name work
	provider.RegisterAlias("google", "gemini")
}

// Name returns the provider identifier.
func (p *Provider) Name() string {
	return "gemini"
}

// ConfigureProxy sets up proxy headers for Gemini API.
func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	if IsOAuthCredential(cred) {
		// OAuth mode: Gemini CLI uses cloudcode-pa.googleapis.com (Cloud Code Private API),
		// NOT generativelanguage.googleapis.com. Inject real Bearer token for the API host.
		proxy.SetCredential(GeminiAPIHost, "Bearer "+cred.Token)

		// The container has a placeholder token in oauth_creds.json. When Gemini CLI
		// validates the token at startup, it POSTs to oauth2.googleapis.com/tokeninfo
		// with the placeholder in the Authorization header. We substitute the real token
		// so Google validates the real credential and returns a real response.
		proxy.SetTokenSubstitution(GeminiOAuthHost, ProxyInjectedPlaceholder, cred.Token)
	} else {
		// API key: use x-goog-api-key header (Gemini API accepts this)
		proxy.SetCredentialHeader(GeminiAPIKeyHost, "x-goog-api-key", cred.Token)
	}
}

// ContainerEnv returns environment variables for Gemini.
func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	if IsOAuthCredential(cred) {
		// OAuth mode: no env vars needed — auth is handled via oauth_creds.json
		// and proxy credential injection.
		return nil
	}
	// API key mode: set GEMINI_API_KEY with a placeholder to skip auth prompts.
	// The real credential is injected by the proxy at the network layer.
	return []string{"GEMINI_API_KEY=" + ProxyInjectedPlaceholder}
}

// ContainerMounts returns mounts needed for Gemini.
// This method returns empty because Gemini setup uses the staging directory
// approach instead of direct mounts.
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

// CanRefresh reports whether this credential can be refreshed.
// OAuth tokens can be refreshed; API keys cannot.
func (p *Provider) CanRefresh(cred *provider.Credential) bool {
	return IsOAuthCredential(cred) && cred.Metadata["refresh_token"] != ""
}

// RefreshInterval returns how often to attempt refresh.
// Google OAuth tokens expire after 1 hour; refresh 15 minutes before.
func (p *Provider) RefreshInterval() time.Duration {
	return 45 * time.Minute
}

// Refresh re-acquires a fresh token and updates the proxy.
func (p *Provider) Refresh(ctx context.Context, proxy provider.ProxyConfigurer, cred *provider.Credential) (*provider.Credential, error) {
	if !p.CanRefresh(cred) {
		return nil, provider.ErrRefreshNotSupported
	}

	refreshToken := cred.Metadata["refresh_token"]
	refresher := &TokenRefresher{}
	result, err := refresher.Refresh(ctx, refreshToken)
	if err != nil {
		var oauthErr *OAuthError
		if errors.As(err, &oauthErr) && oauthErr.IsRevoked() {
			return nil, fmt.Errorf("%w: %w", provider.ErrTokenRevoked, err)
		}
		return nil, err
	}

	// Update proxy with new token
	proxy.SetCredential(GeminiAPIHost, "Bearer "+result.AccessToken)
	proxy.SetTokenSubstitution(GeminiOAuthHost, ProxyInjectedPlaceholder, result.AccessToken)

	// Return updated credential
	newCred := *cred
	newCred.Token = result.AccessToken
	newCred.ExpiresAt = result.ExpiresAt
	return &newCred, nil
}

// Cleanup cleans up Gemini resources.
func (p *Provider) Cleanup(cleanupPath string) {
	// Nothing to clean up - staging directory is handled by the caller
}

// ImpliedDependencies returns dependencies implied by the Gemini provider.
func (p *Provider) ImpliedDependencies() []string {
	return nil
}
