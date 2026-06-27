package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

func TestResolveCredName(t *testing.T) {
	tests := []struct {
		grantName string
		grant     string
		want      credential.Provider
	}{
		{"github", "github", "github"},
		{"oauth", "oauth:notion", "oauth:notion"},
		// MCP grants resolve to the full grant name verbatim so that a
		// credential granted as "mcp:context7" and one granted as the
		// deprecated "mcp-context7" each resolve to their own store entry.
		{"mcp", "mcp:context7", "mcp:context7"},
		{"mcp-context7", "mcp-context7", "mcp-context7"},
	}
	for _, tt := range tests {
		t.Run(tt.grant, func(t *testing.T) {
			got := resolveCredName(tt.grantName, tt.grant)
			if got != tt.want {
				t.Errorf("resolveCredName(%q, %q) = %q, want %q", tt.grantName, tt.grant, got, tt.want)
			}
		})
	}
}

// Regression: the shared daemon may run under the default profile
// (ActiveProfile == "") while serving a run created under a different profile.
// Token refresh must read/write the *run's* profile store, not the daemon
// process's, or it injects the wrong profile's credentials into the run
// (e.g. a profile=vibrant run suddenly using the default profile's Linear auth).
func TestStoreDirForRun_ScopesToRunProfile(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	saved := credential.ActiveProfile
	credential.ActiveProfile = "" // daemon process: default profile
	t.Cleanup(func() { credential.ActiveProfile = saved })

	rc := NewRunContext("run-vibrant")
	rc.CredProfile = "vibrant"

	got := storeDirForRun(rc)
	if want := credential.StoreDirForProfile("vibrant"); got != want {
		t.Errorf("storeDirForRun(profile=vibrant) = %q, want %q", got, want)
	}
	if got == credential.DefaultStoreDir() {
		t.Errorf("storeDirForRun returned the daemon's default store %q — cross-profile credential leak", got)
	}
}

// Companion (other direction): a default-profile run (empty CredProfile, the
// backwards-compat case for an older CLI that omits the profile) must resolve
// to the unscoped default store, never a "profiles/" subdir.
func TestStoreDirForRun_DefaultProfileFallback(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	saved := credential.ActiveProfile
	credential.ActiveProfile = "" // daemon process default
	t.Cleanup(func() { credential.ActiveProfile = saved })

	rc := NewRunContext("run-default")
	rc.CredProfile = "" // older CLI omitted the profile

	got := storeDirForRun(rc)
	if want := credential.DefaultStoreDir(); got != want {
		t.Errorf("storeDirForRun(profile=\"\") = %q, want default store %q", got, want)
	}
	if strings.Contains(got, "profiles") {
		t.Errorf("default-profile run resolved to a profile-scoped dir %q", got)
	}
}

// The run's profile must survive the RegisterRequest -> RunContext conversion
// so the daemon can scope refresh to it.
func TestToRunContext_CarriesProfile(t *testing.T) {
	req := &RegisterRequest{RunID: "r", CredProfile: "vibrant"}
	if got := req.ToRunContext().CredProfile; got != "vibrant" {
		t.Errorf("ToRunContext().CredProfile = %q, want vibrant", got)
	}
}

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
