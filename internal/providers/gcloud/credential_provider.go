package gcloud

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/impersonate"
)

// credentialRefreshBuffer is the time before expiration when tokens should be refreshed.
const credentialRefreshBuffer = 5 * time.Minute

// CredentialProvider manages Google Cloud token fetching and caching.
// It wraps an oauth2.TokenSource and adds caching with a refresh buffer.
type CredentialProvider struct {
	cfg    *Config
	source oauth2.TokenSource
	mu     sync.Mutex
	cached *oauth2.Token
}

// NewCredentialProvider creates a new Google Cloud credential provider
// using Application Default Credentials, a key file, or impersonation.
func NewCredentialProvider(ctx context.Context, cfg *Config) (*CredentialProvider, error) {
	source, err := buildTokenSource(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("building token source: %w", err)
	}
	return &CredentialProvider{
		cfg:    cfg,
		source: source,
	}, nil
}

// NewCredentialProviderFromTokenSource creates a CredentialProvider from a
// custom token source. This is intended for testing.
func NewCredentialProviderFromTokenSource(ts oauth2.TokenSource, cfg *Config) *CredentialProvider {
	return &CredentialProvider{
		cfg:    cfg,
		source: ts,
	}
}

// buildTokenSource constructs the appropriate oauth2.TokenSource for the given config.
func buildTokenSource(ctx context.Context, cfg *Config) (oauth2.TokenSource, error) {
	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{DefaultScope}
	}

	// Key file takes precedence.
	if cfg.KeyFile != "" {
		data, err := os.ReadFile(cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("reading key file %s: %w", cfg.KeyFile, err)
		}
		creds, err := google.CredentialsFromJSON(ctx, data, scopes...)
		if err != nil {
			return nil, fmt.Errorf("parsing key file: %w", err)
		}
		return creds.TokenSource, nil
	}

	// Impersonation wraps the default credentials.
	if cfg.ImpersonateSA != "" {
		ts, err := impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
			TargetPrincipal: cfg.ImpersonateSA,
			Scopes:          scopes,
		})
		if err != nil {
			return nil, fmt.Errorf("impersonating %s: %w", cfg.ImpersonateSA, err)
		}
		return ts, nil
	}

	// Default: Application Default Credentials.
	creds, err := google.FindDefaultCredentials(ctx, scopes...)
	if err != nil {
		return nil, fmt.Errorf("finding default credentials: %w", err)
	}
	return creds.TokenSource, nil
}

// GetToken returns a valid access token, refreshing if necessary.
func (p *CredentialProvider) GetToken(ctx context.Context) (*oauth2.Token, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Return cached if still valid with buffer.
	if p.cached != nil && p.cached.Valid() && time.Now().Add(credentialRefreshBuffer).Before(p.cached.Expiry) {
		return p.cached, nil
	}

	tok, err := p.source.Token()
	if err != nil {
		return nil, fmt.Errorf("fetching token: %w", err)
	}

	p.cached = tok
	return tok, nil
}

// ProjectID returns the configured project ID.
func (p *CredentialProvider) ProjectID() string {
	return p.cfg.ProjectID
}

// Scopes returns the configured OAuth scopes.
func (p *CredentialProvider) Scopes() []string {
	return p.cfg.Scopes
}

// Email returns the configured email identity.
func (p *CredentialProvider) Email() string {
	return p.cfg.Email
}
