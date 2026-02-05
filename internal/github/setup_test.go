package github

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/credential"
)

// mockProxyConfigurer implements credential.ProxyConfigurer for testing.
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

func (m *mockProxyConfigurer) AddExtraHeader(host, headerName, headerValue string) {}

func (m *mockProxyConfigurer) AddResponseTransformer(host string, transformer credential.ResponseTransformer) {
}

func TestSetup_Provider(t *testing.T) {
	s := &Setup{}
	if s.Provider() != credential.ProviderGitHub {
		t.Errorf("Provider() = %v, want %v", s.Provider(), credential.ProviderGitHub)
	}
}

func TestSetup_ConfigureProxy(t *testing.T) {
	s := &Setup{}
	p := newMockProxy()
	cred := &credential.Credential{Token: "test-token"}

	s.ConfigureProxy(p, cred)

	if p.credentials["api.github.com"] != "Bearer test-token" {
		t.Errorf("api.github.com credential = %q, want %q", p.credentials["api.github.com"], "Bearer test-token")
	}
	if p.credentials["github.com"] != "Bearer test-token" {
		t.Errorf("github.com credential = %q, want %q", p.credentials["github.com"], "Bearer test-token")
	}
}

func TestSetup_ContainerEnv(t *testing.T) {
	s := &Setup{}
	cred := &credential.Credential{Token: "test-token"}

	env := s.ContainerEnv(cred)
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

func TestSetup_ContainerMounts(t *testing.T) {
	s := &Setup{}
	cred := &credential.Credential{Token: "test-token"}

	mounts, cleanupPath, err := s.ContainerMounts(cred, "/home/user")
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

		s.Cleanup(cleanupPath)

		if _, err := os.Stat(cleanupPath); !os.IsNotExist(err) {
			t.Error("Cleanup() did not remove the temp directory")
		}
	} else {
		if cleanupPath != "" {
			t.Errorf("cleanupPath should be empty when no mounts, got %q", cleanupPath)
		}
	}
}

func TestSetup_CanRefresh(t *testing.T) {
	s := &Setup{}

	tests := []struct {
		name string
		cred *credential.Credential
		want bool
	}{
		{
			name: "CLI source is refreshable",
			cred: &credential.Credential{
				Metadata: map[string]string{credential.MetaKeyTokenSource: SourceCLI},
			},
			want: true,
		},
		{
			name: "env source is refreshable",
			cred: &credential.Credential{
				Metadata: map[string]string{credential.MetaKeyTokenSource: SourceEnv},
			},
			want: true,
		},
		{
			name: "PAT source is not refreshable",
			cred: &credential.Credential{
				Metadata: map[string]string{credential.MetaKeyTokenSource: SourcePAT},
			},
			want: false,
		},
		{
			name: "nil metadata is not refreshable",
			cred: &credential.Credential{},
			want: false,
		},
		{
			name: "empty metadata is not refreshable",
			cred: &credential.Credential{
				Metadata: map[string]string{},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := s.CanRefresh(tt.cred); got != tt.want {
				t.Errorf("CanRefresh() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetup_RefreshInterval(t *testing.T) {
	s := &Setup{}
	if got := s.RefreshInterval(); got != 30*time.Minute {
		t.Errorf("RefreshInterval() = %v, want %v", got, 30*time.Minute)
	}
}

func TestSetup_RefreshCredential_EnvSource(t *testing.T) {
	s := &Setup{}
	p := newMockProxy()

	cred := &credential.Credential{
		Provider: credential.ProviderGitHub,
		Token:    "old-token",
		Metadata: map[string]string{credential.MetaKeyTokenSource: SourceEnv},
	}

	// Set GITHUB_TOKEN for the test
	t.Setenv("GITHUB_TOKEN", "new-env-token")

	updated, err := s.RefreshCredential(context.Background(), p, cred)
	if err != nil {
		t.Fatalf("RefreshCredential() error = %v", err)
	}

	if updated.Token != "new-env-token" {
		t.Errorf("updated token = %q, want %q", updated.Token, "new-env-token")
	}

	// Verify proxy was updated
	if p.credentials["api.github.com"] != "Bearer new-env-token" {
		t.Errorf("proxy api.github.com = %q, want %q", p.credentials["api.github.com"], "Bearer new-env-token")
	}
	if p.credentials["github.com"] != "Bearer new-env-token" {
		t.Errorf("proxy github.com = %q, want %q", p.credentials["github.com"], "Bearer new-env-token")
	}

	// Verify original credential is not mutated
	if cred.Token != "old-token" {
		t.Errorf("original cred.Token = %q, want %q (should not be mutated)", cred.Token, "old-token")
	}
}

func TestSetup_RefreshCredential_EnvSource_GHToken(t *testing.T) {
	s := &Setup{}
	p := newMockProxy()

	cred := &credential.Credential{
		Provider: credential.ProviderGitHub,
		Token:    "old-token",
		Metadata: map[string]string{credential.MetaKeyTokenSource: SourceEnv},
	}

	// Only GH_TOKEN set (GITHUB_TOKEN unset)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "gh-token-value")

	updated, err := s.RefreshCredential(context.Background(), p, cred)
	if err != nil {
		t.Fatalf("RefreshCredential() error = %v", err)
	}

	if updated.Token != "gh-token-value" {
		t.Errorf("updated token = %q, want %q", updated.Token, "gh-token-value")
	}
}

func TestSetup_RefreshCredential_EnvSource_Empty(t *testing.T) {
	s := &Setup{}
	p := newMockProxy()

	cred := &credential.Credential{
		Provider: credential.ProviderGitHub,
		Token:    "old-token",
		Metadata: map[string]string{credential.MetaKeyTokenSource: SourceEnv},
	}

	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	_, err := s.RefreshCredential(context.Background(), p, cred)
	if err == nil {
		t.Error("RefreshCredential() should error when env vars are empty")
	}
}

func TestSetup_RefreshCredential_UnsupportedSource(t *testing.T) {
	s := &Setup{}
	p := newMockProxy()

	cred := &credential.Credential{
		Provider: credential.ProviderGitHub,
		Token:    "old-token",
		Metadata: map[string]string{credential.MetaKeyTokenSource: SourcePAT},
	}

	_, err := s.RefreshCredential(context.Background(), p, cred)
	if err == nil {
		t.Error("RefreshCredential() should error for PAT source")
	}
}

func TestSetup_InitRegistration(t *testing.T) {
	// Verify that init() registered the GitHub provider
	setup := credential.GetProviderSetup(credential.ProviderGitHub)
	if setup == nil {
		t.Fatal("GitHub provider not registered via init()")
	}
	if setup.Provider() != credential.ProviderGitHub {
		t.Errorf("Provider() = %v, want %v", setup.Provider(), credential.ProviderGitHub)
	}

	// Verify implied deps were registered
	deps := credential.ImpliedDependencies([]string{"github"})
	if len(deps) != 2 || deps[0] != "gh" || deps[1] != "git" {
		t.Errorf("ImpliedDependencies([github]) = %v, want [gh git]", deps)
	}
}
