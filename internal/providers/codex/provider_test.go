package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/provider"
)

// mockProxyConfigurer implements provider.ProxyConfigurer for testing.
type mockProxyConfigurer struct {
	credentials map[string]string
	headers     map[string]map[string]string
}

func newMockProxyConfigurer() *mockProxyConfigurer {
	return &mockProxyConfigurer{
		credentials: make(map[string]string),
		headers:     make(map[string]map[string]string),
	}
}

func (m *mockProxyConfigurer) SetCredential(host, value string) {
	m.credentials[host] = value
}

func (m *mockProxyConfigurer) SetCredentialHeader(host, headerName, headerValue string) {
	if m.headers[host] == nil {
		m.headers[host] = make(map[string]string)
	}
	m.headers[host][headerName] = headerValue
}

func (m *mockProxyConfigurer) SetCredentialWithGrant(host, headerName, headerValue, grant string) {
	if m.headers[host] == nil {
		m.headers[host] = make(map[string]string)
	}
	m.headers[host][headerName] = headerValue
}

func (m *mockProxyConfigurer) AddExtraHeader(host, headerName, headerValue string) {
	if m.headers[host] == nil {
		m.headers[host] = make(map[string]string)
	}
	m.headers[host][headerName] = headerValue
}

func (m *mockProxyConfigurer) AddResponseTransformer(host string, transformer provider.ResponseTransformer) {
	// Not used in these tests
}

func (m *mockProxyConfigurer) RemoveRequestHeader(host, header string) {}

func (m *mockProxyConfigurer) SetTokenSubstitution(host, placeholder, realToken string) {}

func TestProvider_Name(t *testing.T) {
	p := &Provider{}
	if got := p.Name(); got != "codex" {
		t.Errorf("Name() = %q, want %q", got, "codex")
	}
}

func TestProvider_ConfigureProxy(t *testing.T) {
	p := &Provider{}
	proxy := newMockProxyConfigurer()
	cred := &provider.Credential{
		Provider: "codex",
		Token:    "sk-test-api-key-12345",
	}

	p.ConfigureProxy(proxy, cred)

	// Check that api.openai.com has the Bearer token (stored as "Header: Value")
	want := "Bearer sk-test-api-key-12345"
	if got := proxy.headers["api.openai.com"]["Authorization"]; got != want {
		t.Errorf("api.openai.com Authorization header = %q, want %q", got, want)
	}
}

func TestProvider_ContainerEnv(t *testing.T) {
	p := &Provider{}
	cred := &provider.Credential{
		Provider: "codex",
		Token:    "sk-test-api-key-12345",
	}

	env := p.ContainerEnv(cred)

	if len(env) != 1 {
		t.Fatalf("ContainerEnv() returned %d items, want 1", len(env))
	}

	expected := "OPENAI_API_KEY=" + OpenAIAPIKeyPlaceholder
	if env[0] != expected {
		t.Errorf("ContainerEnv()[0] = %q, want %q", env[0], expected)
	}
}

func TestProvider_ContainerMounts(t *testing.T) {
	p := &Provider{}
	cred := &provider.Credential{
		Provider: "codex",
		Token:    "sk-test-api-key-12345",
	}

	mounts, cleanupPath, err := p.ContainerMounts(cred, "/home/testuser")

	if err != nil {
		t.Errorf("ContainerMounts() error = %v", err)
	}
	if mounts != nil {
		t.Errorf("ContainerMounts() mounts = %v, want nil", mounts)
	}
	if cleanupPath != "" {
		t.Errorf("ContainerMounts() cleanupPath = %q, want empty", cleanupPath)
	}
}

func TestProvider_ImpliedDependencies(t *testing.T) {
	p := &Provider{}

	deps := p.ImpliedDependencies()

	if deps != nil {
		t.Errorf("ImpliedDependencies() = %v, want nil", deps)
	}
}

func TestPopulateStagingDir(t *testing.T) {
	tmpDir := t.TempDir()

	cred := &provider.Credential{
		Provider:  "codex",
		Token:     "sk-test-api-key-12345",
		CreatedAt: time.Now(),
	}

	err := PopulateStagingDir(cred, tmpDir)
	if err != nil {
		t.Fatalf("PopulateStagingDir() error = %v", err)
	}

	// Check auth.json exists
	authPath := filepath.Join(tmpDir, "auth.json")
	data, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("reading auth.json: %v", err)
	}

	// Verify content contains placeholder, not real key
	content := string(data)
	if !contains(content, OpenAIAPIKeyPlaceholder) {
		t.Errorf("auth.json should contain placeholder key, got: %s", content)
	}
	if contains(content, "sk-test-api-key-12345") {
		t.Errorf("auth.json should NOT contain real API key")
	}
}

func TestWriteCodexConfig(t *testing.T) {
	tmpDir := t.TempDir()

	err := WriteCodexConfig(tmpDir)
	if err != nil {
		t.Fatalf("WriteCodexConfig() error = %v", err)
	}

	// Check config.toml exists
	configPath := filepath.Join(tmpDir, "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("reading config.toml: %v", err)
	}

	// Verify content has shell_environment_policy
	content := string(data)
	if !contains(content, "[shell_environment_policy]") {
		t.Errorf("config.toml should contain [shell_environment_policy], got: %s", content)
	}
}

func TestGenerateMCPConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *config.Config
		grants  []string
		wantNil bool
		wantErr bool
	}{
		{
			name:    "nil config",
			cfg:     nil,
			grants:  []string{},
			wantNil: true,
			wantErr: false,
		},
		{
			name: "empty MCP servers",
			cfg: &config.Config{
				Codex: config.CodexConfig{
					MCP: map[string]config.MCPServerSpec{},
				},
			},
			grants:  []string{},
			wantNil: true,
			wantErr: false,
		},
		{
			name: "MCP server with missing grant",
			cfg: &config.Config{
				Codex: config.CodexConfig{
					MCP: map[string]config.MCPServerSpec{
						"test": {
							Command: "test-cmd",
							Grant:   "github",
						},
					},
				},
			},
			grants:  []string{}, // github grant not provided
			wantNil: false,
			wantErr: true,
		},
		{
			name: "MCP server with grant",
			cfg: &config.Config{
				Codex: config.CodexConfig{
					MCP: map[string]config.MCPServerSpec{
						"test": {
							Command: "test-cmd",
							Grant:   "github",
						},
					},
				},
			},
			grants:  []string{"github"},
			wantNil: false,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GenerateMCPConfig(tt.cfg, tt.grants)

			if (err != nil) != tt.wantErr {
				t.Errorf("GenerateMCPConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantNil && got != nil {
				t.Errorf("GenerateMCPConfig() = %v, want nil", got)
			}

			if !tt.wantNil && !tt.wantErr && got == nil {
				t.Errorf("GenerateMCPConfig() = nil, want non-nil")
			}
		})
	}
}

func TestNetworkHosts(t *testing.T) {
	hosts := NetworkHosts()

	if len(hosts) == 0 {
		t.Error("NetworkHosts() returned empty slice")
	}

	// Check for essential hosts
	hasOpenAI := false
	for _, h := range hosts {
		if h == "api.openai.com" {
			hasOpenAI = true
		}
	}

	if !hasOpenAI {
		t.Error("NetworkHosts() should include api.openai.com")
	}
}

func TestDefaultDependencies(t *testing.T) {
	deps := DefaultDependencies()

	if len(deps) == 0 {
		t.Error("DefaultDependencies() returned empty slice")
	}

	// Check for essential dependencies
	hasNode := false
	hasCodexCLI := false
	for _, d := range deps {
		if contains(d, "node") {
			hasNode = true
		}
		if d == "codex-cli" {
			hasCodexCLI = true
		}
	}

	if !hasNode {
		t.Error("DefaultDependencies() should include node")
	}
	if !hasCodexCLI {
		t.Error("DefaultDependencies() should include codex-cli")
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
