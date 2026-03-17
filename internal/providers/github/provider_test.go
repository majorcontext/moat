package github

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestProvider_ContainerMounts_ReturnsNothing(t *testing.T) {
	p := &Provider{}
	cred := &provider.Credential{Token: "test-token"}

	mounts, cleanupPath, err := p.ContainerMounts(cred, "/home/user")
	if err != nil {
		t.Fatalf("ContainerMounts() error = %v", err)
	}
	if len(mounts) != 0 {
		t.Errorf("ContainerMounts() returned %d mounts, want 0", len(mounts))
	}
	if cleanupPath != "" {
		t.Errorf("ContainerMounts() cleanupPath = %q, want empty", cleanupPath)
	}
}

func TestProvider_ContainerInitFiles(t *testing.T) {
	p := &Provider{}
	cred := &provider.Credential{Token: "test-token"}

	// Create a temp gh config to test with
	tmpHome := t.TempDir()
	ghConfigDir := filepath.Join(tmpHome, ".config", "gh")
	if err := os.MkdirAll(ghConfigDir, 0700); err != nil {
		t.Fatal(err)
	}
	configContent := "git_protocol: ssh\neditor: vim\n"
	if err := os.WriteFile(filepath.Join(ghConfigDir, "config.yml"), []byte(configContent), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", tmpHome)

	files := p.ContainerInitFiles(cred, "/home/user")
	if files == nil {
		t.Fatal("ContainerInitFiles() returned nil, expected config file")
	}

	wantPath := "/home/user/.config/gh/config.yml"
	content, ok := files[wantPath]
	if !ok {
		t.Fatalf("ContainerInitFiles() missing key %q, got keys: %v", wantPath, files)
	}
	// Content goes through YAML parse/marshal so field order may differ
	if !strings.Contains(content, "git_protocol: ssh") {
		t.Errorf("config content missing git_protocol, got: %q", content)
	}
	if !strings.Contains(content, "editor: vim") {
		t.Errorf("config content missing editor, got: %q", content)
	}
}

func TestProvider_ContainerInitFiles_StripsCredentials(t *testing.T) {
	p := &Provider{}
	cred := &provider.Credential{Token: "test-token"}

	tmpHome := t.TempDir()
	ghConfigDir := filepath.Join(tmpHome, ".config", "gh")
	if err := os.MkdirAll(ghConfigDir, 0700); err != nil {
		t.Fatal(err)
	}
	// Config with embedded credentials (insecure-storage or older gh versions)
	configContent := `git_protocol: ssh
editor: vim
hosts:
  github.com:
    oauth_token: gho_SECRETTOKEN123
    user: testuser
`
	if err := os.WriteFile(filepath.Join(ghConfigDir, "config.yml"), []byte(configContent), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", tmpHome)

	files := p.ContainerInitFiles(cred, "/home/user")
	if files == nil {
		t.Fatal("ContainerInitFiles() returned nil, expected config file")
	}

	content := files["/home/user/.config/gh/config.yml"]

	// Preferences should be preserved
	if !strings.Contains(content, "git_protocol: ssh") {
		t.Errorf("config content missing git_protocol, got: %q", content)
	}
	if !strings.Contains(content, "editor: vim") {
		t.Errorf("config content missing editor, got: %q", content)
	}

	// Credentials must be stripped
	if strings.Contains(content, "hosts") {
		t.Errorf("config content should not contain hosts section, got: %q", content)
	}
	if strings.Contains(content, "oauth_token") {
		t.Errorf("config content should not contain oauth_token, got: %q", content)
	}
	if strings.Contains(content, "SECRET") {
		t.Errorf("config content should not contain token value, got: %q", content)
	}
}

func TestProvider_ContainerInitFiles_CredentialsOnly(t *testing.T) {
	p := &Provider{}
	cred := &provider.Credential{Token: "test-token"}

	tmpHome := t.TempDir()
	ghConfigDir := filepath.Join(tmpHome, ".config", "gh")
	if err := os.MkdirAll(ghConfigDir, 0700); err != nil {
		t.Fatal(err)
	}
	// Config with only credential fields, no preferences
	configContent := "hosts:\n  github.com:\n    oauth_token: gho_SECRET\n"
	if err := os.WriteFile(filepath.Join(ghConfigDir, "config.yml"), []byte(configContent), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", tmpHome)

	files := p.ContainerInitFiles(cred, "/home/user")
	if files != nil {
		t.Errorf("ContainerInitFiles() = %v, want nil for credential-only config", files)
	}
}

func TestProvider_ContainerInitFiles_NoConfig(t *testing.T) {
	p := &Provider{}
	cred := &provider.Credential{Token: "test-token"}

	// Point HOME to an empty directory
	t.Setenv("HOME", t.TempDir())

	files := p.ContainerInitFiles(cred, "/home/user")
	if files != nil {
		t.Errorf("ContainerInitFiles() = %v, want nil when no gh config exists", files)
	}
}

func TestSanitizeGhConfig(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantNil  bool
		contains []string
		excludes []string
	}{
		{
			name:     "preferences only",
			input:    "git_protocol: ssh\neditor: vim\n",
			contains: []string{"git_protocol: ssh", "editor: vim"},
		},
		{
			name:     "strips hosts section",
			input:    "git_protocol: ssh\nhosts:\n  github.com:\n    oauth_token: secret\n",
			contains: []string{"git_protocol: ssh"},
			excludes: []string{"hosts", "oauth_token", "secret"},
		},
		{
			name:     "strips top-level oauth_token",
			input:    "git_protocol: ssh\noauth_token: secret\n",
			contains: []string{"git_protocol: ssh"},
			excludes: []string{"oauth_token", "secret"},
		},
		{
			name:    "only credentials returns nil",
			input:   "hosts:\n  github.com:\n    oauth_token: secret\n",
			wantNil: true,
		},
		{
			name:     "strips null values",
			input:    "git_protocol: ssh\nhttp_unix_socket: null\npager: less\n",
			contains: []string{"git_protocol: ssh", "pager: less"},
			excludes: []string{"null", "http_unix_socket"},
		},
		{
			name:    "only null values returns nil",
			input:   "http_unix_socket: null\n",
			wantNil: true,
		},
		{
			name:    "invalid yaml returns nil",
			input:   ":\t:\n",
			wantNil: true,
		},
		{
			name:     "strips nil values to prevent YAML null round-trip",
			input:    "git_protocol: https\neditor:\npager:\nbrowser:\nhttp_unix_socket:\n",
			contains: []string{"git_protocol: https"},
			excludes: []string{"null", "http_unix_socket", "editor", "pager", "browser"},
		},
		{
			name:     "strips http_unix_socket even when set",
			input:    "git_protocol: ssh\nhttp_unix_socket: /tmp/gh.sock\n",
			contains: []string{"git_protocol: ssh"},
			excludes: []string{"http_unix_socket", "/tmp/gh.sock"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := sanitizeGhConfig([]byte(tt.input))
			if tt.wantNil {
				if result != nil {
					t.Errorf("sanitizeGhConfig() = %q, want nil", result)
				}
				return
			}
			if err != nil {
				t.Fatalf("sanitizeGhConfig() error = %v", err)
			}
			for _, s := range tt.contains {
				if !strings.Contains(string(result), s) {
					t.Errorf("result missing %q, got: %q", s, result)
				}
			}
			for _, s := range tt.excludes {
				if strings.Contains(string(result), s) {
					t.Errorf("result should not contain %q, got: %q", s, result)
				}
			}
		})
	}
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
