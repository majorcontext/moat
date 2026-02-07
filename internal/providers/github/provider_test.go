package github

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

// mockProxyConfigurer implements provider.ProxyConfigurer for testing.
type mockProxyConfigurer struct {
	credentials map[string]string
}

func newMockProxy() *mockProxyConfigurer {
	return &mockProxyConfigurer{credentials: make(map[string]string)}
}

func (m *mockProxyConfigurer) SetCredential(host, value string) {
	m.credentials[host] = value
}

func (m *mockProxyConfigurer) SetCredentialHeader(host, headerName, headerValue string) {
	m.credentials[host] = headerName + ": " + headerValue
}

func (m *mockProxyConfigurer) SetCredentialWithGrant(host, headerName, headerValue, grant string) {
	m.credentials[host] = headerName + ": " + headerValue
}

func (m *mockProxyConfigurer) AddExtraHeader(host, headerName, headerValue string) {}

func (m *mockProxyConfigurer) AddResponseTransformer(host string, transformer provider.ResponseTransformer) {
}

func (m *mockProxyConfigurer) RemoveRequestHeader(host, header string) {}

func (m *mockProxyConfigurer) SetTokenSubstitution(host, placeholder, realToken string) {}

func TestProvider_Name(t *testing.T) {
	p := &Provider{}
	if got := p.Name(); got != "github" {
		t.Errorf("Name() = %v, want %v", got, "github")
	}
}

func TestProvider_ConfigureProxy(t *testing.T) {
	p := &Provider{}
	proxy := newMockProxy()
	cred := &provider.Credential{Token: "test-token"}

	p.ConfigureProxy(proxy, cred)

	want := "Authorization: Bearer test-token"
	if proxy.credentials["api.github.com"] != want {
		t.Errorf("api.github.com credential = %q, want %q", proxy.credentials["api.github.com"], want)
	}
	if proxy.credentials["github.com"] != want {
		t.Errorf("github.com credential = %q, want %q", proxy.credentials["github.com"], want)
	}
}

func TestProvider_ContainerEnv(t *testing.T) {
	p := &Provider{}
	cred := &provider.Credential{Token: "test-token"}

	env := p.ContainerEnv(cred)
	if len(env) != 2 {
		t.Fatalf("ContainerEnv() returned %d vars, want 2", len(env))
	}

	expectedGHToken := "GH_TOKEN=" + credential.GitHubTokenPlaceholder
	if env[0] != expectedGHToken {
		t.Errorf("ContainerEnv()[0] = %q, want %q", env[0], expectedGHToken)
	}

	expectedGitPrompt := "GIT_TERMINAL_PROMPT=0"
	if env[1] != expectedGitPrompt {
		t.Errorf("ContainerEnv()[1] = %q, want %q", env[1], expectedGitPrompt)
	}
}

func TestProvider_ContainerMounts(t *testing.T) {
	p := &Provider{}
	cred := &provider.Credential{Token: "test-token"}

	mounts, cleanupPath, err := p.ContainerMounts(cred, "/home/user")
	if err != nil {
		t.Fatalf("ContainerMounts() error = %v", err)
	}

	if len(mounts) > 0 {
		if mounts[0].Target != "/home/user/.config/gh" {
			t.Errorf("Mount target = %q, want %q", mounts[0].Target, "/home/user/.config/gh")
		}

		configPath := mounts[0].Source + "/config.yml"
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			t.Error("config.yml was not created")
		}

		// Verify cleanup path is returned
		if cleanupPath == "" {
			t.Error("ContainerMounts() cleanupPath is empty, expected temp directory path")
		}

		// Clean up after test
		if cleanupPath != "" {
			defer os.RemoveAll(cleanupPath)
		}

		// Cleanup
		p.Cleanup(mounts[0].Source)
	}
	// Note: If no mounts returned, that's acceptable (user has no gh config)
}

func TestProvider_CanRefresh(t *testing.T) {
	p := &Provider{}

	tests := []struct {
		name string
		cred *provider.Credential
		want bool
	}{
		{
			name: "CLI source is refreshable",
			cred: &provider.Credential{
				Metadata: map[string]string{provider.MetaKeyTokenSource: SourceCLI},
			},
			want: true,
		},
		{
			name: "env source is refreshable",
			cred: &provider.Credential{
				Metadata: map[string]string{provider.MetaKeyTokenSource: SourceEnv},
			},
			want: true,
		},
		{
			name: "PAT source is not refreshable",
			cred: &provider.Credential{
				Metadata: map[string]string{provider.MetaKeyTokenSource: SourcePAT},
			},
			want: false,
		},
		{
			name: "nil metadata is not refreshable",
			cred: &provider.Credential{},
			want: false,
		},
		{
			name: "empty metadata is not refreshable",
			cred: &provider.Credential{
				Metadata: map[string]string{},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.CanRefresh(tt.cred); got != tt.want {
				t.Errorf("CanRefresh() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProvider_RefreshInterval(t *testing.T) {
	p := &Provider{}
	if got := p.RefreshInterval(); got != 30*time.Minute {
		t.Errorf("RefreshInterval() = %v, want %v", got, 30*time.Minute)
	}
}

func TestProvider_ImpliedDependencies(t *testing.T) {
	p := &Provider{}
	deps := p.ImpliedDependencies()
	if len(deps) != 2 {
		t.Fatalf("ImpliedDependencies() returned %d deps, want 2", len(deps))
	}
	if deps[0] != "gh" {
		t.Errorf("ImpliedDependencies()[0] = %q, want %q", deps[0], "gh")
	}
	if deps[1] != "git" {
		t.Errorf("ImpliedDependencies()[1] = %q, want %q", deps[1], "git")
	}
}

func TestProvider_Refresh_EnvSource(t *testing.T) {
	p := &Provider{}
	proxy := newMockProxy()

	cred := &provider.Credential{
		Provider: "github",
		Token:    "old-token",
		Metadata: map[string]string{provider.MetaKeyTokenSource: SourceEnv},
	}

	// Set GITHUB_TOKEN for the test
	t.Setenv("GITHUB_TOKEN", "new-env-token")

	updated, err := p.Refresh(context.Background(), proxy, cred)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	if updated.Token != "new-env-token" {
		t.Errorf("updated token = %q, want %q", updated.Token, "new-env-token")
	}

	// Verify proxy was updated
	want := "Authorization: Bearer new-env-token"
	if proxy.credentials["api.github.com"] != want {
		t.Errorf("proxy api.github.com = %q, want %q", proxy.credentials["api.github.com"], want)
	}
	if proxy.credentials["github.com"] != want {
		t.Errorf("proxy github.com = %q, want %q", proxy.credentials["github.com"], want)
	}

	// Verify original credential is not mutated
	if cred.Token != "old-token" {
		t.Errorf("original cred.Token = %q, want %q (should not be mutated)", cred.Token, "old-token")
	}
}

func TestProvider_Refresh_EnvSource_GHToken(t *testing.T) {
	p := &Provider{}
	proxy := newMockProxy()

	cred := &provider.Credential{
		Provider: "github",
		Token:    "old-token",
		Metadata: map[string]string{provider.MetaKeyTokenSource: SourceEnv},
	}

	// Only GH_TOKEN set (GITHUB_TOKEN unset)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "gh-token-value")

	updated, err := p.Refresh(context.Background(), proxy, cred)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	if updated.Token != "gh-token-value" {
		t.Errorf("updated token = %q, want %q", updated.Token, "gh-token-value")
	}
}

func TestProvider_Refresh_EnvSource_Empty(t *testing.T) {
	p := &Provider{}
	proxy := newMockProxy()

	cred := &provider.Credential{
		Provider: "github",
		Token:    "old-token",
		Metadata: map[string]string{provider.MetaKeyTokenSource: SourceEnv},
	}

	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	_, err := p.Refresh(context.Background(), proxy, cred)
	if err == nil {
		t.Error("Refresh() should error when env vars are empty")
	}
}

func TestProvider_Refresh_UnsupportedSource(t *testing.T) {
	p := &Provider{}
	proxy := newMockProxy()

	cred := &provider.Credential{
		Provider: "github",
		Token:    "old-token",
		Metadata: map[string]string{provider.MetaKeyTokenSource: SourcePAT},
	}

	_, err := p.Refresh(context.Background(), proxy, cred)
	if err != provider.ErrRefreshNotSupported {
		t.Errorf("Refresh() error = %v, want %v", err, provider.ErrRefreshNotSupported)
	}
}

func TestProvider_Refresh_NilMetadata(t *testing.T) {
	p := &Provider{}
	proxy := newMockProxy()

	cred := &provider.Credential{
		Provider: "github",
		Token:    "old-token",
		Metadata: nil,
	}

	_, err := p.Refresh(context.Background(), proxy, cred)
	if err != provider.ErrRefreshNotSupported {
		t.Errorf("Refresh() error = %v, want %v", err, provider.ErrRefreshNotSupported)
	}
}

func TestProvider_InitRegistration(t *testing.T) {
	// Verify that init() registered the GitHub provider
	p := provider.Get("github")
	if p == nil {
		t.Fatal("GitHub provider not registered via init()")
	}
	if p.Name() != "github" {
		t.Errorf("Name() = %v, want %v", p.Name(), "github")
	}
}
