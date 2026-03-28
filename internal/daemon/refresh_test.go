package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

func TestRefreshTokensForRun_NoRefreshableProviders(t *testing.T) {
	// With no providers registered (test environment), refreshTokensForRun
	// should be a no-op and not panic.
	rc := NewRunContext("test-run")
	store := &nullStore{}

	// Should complete without error or panic.
	refreshTokensForRun(context.Background(), rc, []string{"github", "claude"}, store)
}

func TestStartTokenRefresh_NoRefreshable(t *testing.T) {
	// With no refreshable providers, StartTokenRefresh should return
	// immediately without spawning a goroutine.
	rc := NewRunContext("test-run")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Should return immediately — no goroutine spawned since no providers
	// are registered in the test environment.
	StartTokenRefresh(ctx, rc, []string{"github", "claude"})
}

func TestStartTokenRefresh_EmptyGrants(t *testing.T) {
	rc := NewRunContext("test-run")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Empty grants should be a no-op.
	StartTokenRefresh(ctx, rc, nil)
}

func TestRefreshTokensForRun_SkipsSSH(t *testing.T) {
	// SSH grants should be skipped entirely.
	rc := NewRunContext("test-run")
	store := &nullStore{}

	// Should complete without error — "ssh" is skipped before provider lookup.
	refreshTokensForRun(context.Background(), rc, []string{"ssh"}, store)
}

func TestRefreshTokensForRun_UpdatesMCPCredentials(t *testing.T) {
	// Register a fake refreshable provider.
	const provName = "testrefresh"
	const grantName = "testrefresh:myserver"
	const oldToken = "old-token"
	const newToken = "new-token"

	fake := &fakeRefreshableProvider{
		name:     provName,
		newToken: newToken,
	}
	provider.Register(fake)
	t.Cleanup(func() { provider.Unregister(provName) })

	// Create a RunContext with an MCP server that uses our grant.
	rc := NewRunContext("test-run")
	rc.MCPServers = []config.MCPServerConfig{
		{
			Name: "myserver",
			URL:  "https://mcp.example.com/sse",
			Auth: &config.MCPAuthConfig{
				Grant:  grantName,
				Header: "Authorization",
			},
		},
	}
	// Pre-populate the credential as initial setup would.
	rc.SetCredentialWithGrant("mcp.example.com", "Authorization", oldToken, grantName)

	// Set up a store that returns the old credential.
	// resolveCredName maps non-oauth providers to the canonical name (provName),
	// not the full grant name.
	store := &fakeStore{
		creds: map[credential.Provider]*credential.Credential{
			credential.Provider(provName): {
				Provider:  credential.Provider(provName),
				Token:     oldToken,
				ExpiresAt: time.Now().Add(1 * time.Hour),
				CreatedAt: time.Now(),
			},
		},
	}

	refreshTokensForRun(context.Background(), rc, []string{grantName}, store)

	// Verify the MCP server credential was updated.
	entry, ok := rc.GetCredential("mcp.example.com")
	if !ok {
		t.Fatal("expected credential for mcp.example.com")
	}
	if entry.Value != newToken {
		t.Errorf("MCP credential value = %q, want %q", entry.Value, newToken)
	}
	if entry.Grant != grantName {
		t.Errorf("MCP credential grant = %q, want %q", entry.Grant, grantName)
	}

	// Verify the credential was persisted to the store (keyed by resolved name).
	saved, err := store.Get(credential.Provider(provName))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Token != newToken {
		t.Errorf("stored token = %q, want %q", saved.Token, newToken)
	}
}

// fakeRefreshableProvider implements CredentialProvider + RefreshableProvider.
type fakeRefreshableProvider struct {
	name     string
	newToken string
}

func (f *fakeRefreshableProvider) Name() string { return f.name }
func (f *fakeRefreshableProvider) Grant(context.Context) (*provider.Credential, error) {
	return nil, nil
}
func (f *fakeRefreshableProvider) ConfigureProxy(provider.ProxyConfigurer, *provider.Credential) {}
func (f *fakeRefreshableProvider) ContainerEnv(*provider.Credential) []string                    { return nil }
func (f *fakeRefreshableProvider) ContainerMounts(*provider.Credential, string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}
func (f *fakeRefreshableProvider) Cleanup(string)                {}
func (f *fakeRefreshableProvider) ImpliedDependencies() []string { return nil }
func (f *fakeRefreshableProvider) CanRefresh(*provider.Credential) bool {
	return true
}
func (f *fakeRefreshableProvider) RefreshInterval() time.Duration {
	return 5 * time.Minute
}
func (f *fakeRefreshableProvider) Refresh(_ context.Context, _ provider.ProxyConfigurer, old *provider.Credential) (*provider.Credential, error) {
	return &provider.Credential{
		Token:     f.newToken,
		ExpiresAt: old.ExpiresAt,
		CreatedAt: old.CreatedAt,
	}, nil
}

// nullStore is a minimal credential.Store that returns errors for all operations.
// Used in tests where no actual credential access is expected.
type nullStore struct{}

func (s *nullStore) Save(_ credential.Credential) error                        { return nil }
func (s *nullStore) Get(_ credential.Provider) (*credential.Credential, error) { return nil, nil }
func (s *nullStore) Delete(_ credential.Provider) error                        { return nil }
func (s *nullStore) List() ([]credential.Credential, error)                    { return nil, nil }

// fakeStore is a credential.Store backed by an in-memory map.
type fakeStore struct {
	creds map[credential.Provider]*credential.Credential
}

func (s *fakeStore) Save(c credential.Credential) error {
	s.creds[c.Provider] = &c
	return nil
}
func (s *fakeStore) Get(p credential.Provider) (*credential.Credential, error) {
	c, ok := s.creds[p]
	if !ok {
		return nil, nil
	}
	return c, nil
}
func (s *fakeStore) Delete(_ credential.Provider) error     { return nil }
func (s *fakeStore) List() ([]credential.Credential, error) { return nil, nil }
