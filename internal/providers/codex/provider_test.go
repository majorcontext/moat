package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

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

// writeAndParseConfig writes cfg to a temp staging dir and reads config.toml
// back through a TOML parser, so assertions test what Codex would load rather
// than how it happens to be formatted.
func writeAndParseConfig(t *testing.T, cfg Config) Config {
	t.Helper()

	tmpDir := t.TempDir()
	if err := WriteCodexConfig(tmpDir, cfg); err != nil {
		t.Fatalf("WriteCodexConfig() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "config.toml"))
	if err != nil {
		t.Fatalf("reading config.toml: %v", err)
	}

	var got Config
	if err := toml.Unmarshal(data, &got); err != nil {
		t.Fatalf("config.toml is not valid TOML: %v\n%s", err, data)
	}
	return got
}

func TestWriteCodexConfig_Defaults(t *testing.T) {
	got := writeAndParseConfig(t, NewConfig(false))

	// Approvals and Codex's own sandbox are off: the container is the
	// isolation boundary, and Codex's sandbox would block the moat proxy.
	if got.ApprovalPolicy != ApprovalNever {
		t.Errorf("approval_policy = %q, want %q", got.ApprovalPolicy, ApprovalNever)
	}
	if got.SandboxMode != SandboxFullAccess {
		t.Errorf("sandbox_mode = %q, want %q", got.SandboxMode, SandboxFullAccess)
	}
	// "core" would strip HTTP_PROXY/SSL_CERT_FILE from commands Codex runs.
	if got.ShellEnvironmentPolicy.Inherit != "all" {
		t.Errorf("shell_environment_policy.inherit = %q, want all", got.ShellEnvironmentPolicy.Inherit)
	}
	if lvl := got.Projects[WorkspacePath].TrustLevel; lvl != "trusted" {
		t.Errorf("projects[%q].trust_level = %q, want trusted", WorkspacePath, lvl)
	}
	if got.MCPServers != nil {
		t.Errorf("expected no mcp_servers by default, got %v", got.MCPServers)
	}
}

func TestWriteCodexConfig_RequireApproval(t *testing.T) {
	got := writeAndParseConfig(t, NewConfig(true))

	// --noyolo restores Codex's own defaults...
	if got.ApprovalPolicy != ApprovalOnRequest {
		t.Errorf("approval_policy = %q, want %q", got.ApprovalPolicy, ApprovalOnRequest)
	}
	if got.SandboxMode != SandboxWorkspaceWrite {
		t.Errorf("sandbox_mode = %q, want %q", got.SandboxMode, SandboxWorkspaceWrite)
	}
	// ...but not the environment policy or the trust entry, which are about
	// moat's container setup rather than how much the agent may do.
	if got.ShellEnvironmentPolicy.Inherit != "all" {
		t.Errorf("shell_environment_policy.inherit = %q, want all", got.ShellEnvironmentPolicy.Inherit)
	}
	if lvl := got.Projects[WorkspacePath].TrustLevel; lvl != "trusted" {
		t.Errorf("projects[%q].trust_level = %q, want trusted", WorkspacePath, lvl)
	}
}

func TestWriteCodexConfig_MCPServers(t *testing.T) {
	cfg := NewConfig(false)
	cfg.MCPServers = map[string]MCPServer{
		"relay": {URL: "http://proxy:8080/mcp/tok/relay", HTTPHeaders: map[string]string{"Authorization": "moat-stub-github"}},
		"local": {Command: "run-server", Args: []string{"-x"}, Env: map[string]string{"K": "v"}, Cwd: "/workspace"},
	}

	got := writeAndParseConfig(t, cfg)

	relay, ok := got.MCPServers["relay"]
	if !ok {
		t.Fatalf("mcp_servers.relay missing, got %v", got.MCPServers)
	}
	if relay.URL != "http://proxy:8080/mcp/tok/relay" {
		t.Errorf("relay url = %q", relay.URL)
	}
	if relay.HTTPHeaders["Authorization"] != "moat-stub-github" {
		t.Errorf("relay http_headers = %v", relay.HTTPHeaders)
	}
	if relay.Command != "" {
		t.Errorf("remote server should not have a command, got %q", relay.Command)
	}

	local, ok := got.MCPServers["local"]
	if !ok {
		t.Fatalf("mcp_servers.local missing, got %v", got.MCPServers)
	}
	if local.Command != "run-server" || len(local.Args) != 1 || local.Cwd != "/workspace" {
		t.Errorf("local server not round-tripped: %+v", local)
	}
	if local.Env["K"] != "v" {
		t.Errorf("local env = %v", local.Env)
	}
	if local.URL != "" {
		t.Errorf("local server should not have a url, got %q", local.URL)
	}
}

func TestBuildMCPServers(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		got, err := buildMCPServers(provider.PrepareOpts{})
		if err != nil || got != nil {
			t.Fatalf("expected (nil, nil), got (%v, %v)", got, err)
		}
	})

	t.Run("remote and local merge", func(t *testing.T) {
		got, err := buildMCPServers(provider.PrepareOpts{
			MCPServers: map[string]provider.MCPServerConfig{
				"remote": {URL: "http://relay/mcp", Headers: map[string]string{"Authorization": "stub"}},
			},
			LocalMCPServers: map[string]provider.LocalMCPServerConfig{
				"local": {Command: "srv"},
			},
		})
		if err != nil {
			t.Fatalf("buildMCPServers() error = %v", err)
		}
		if got["remote"].URL != "http://relay/mcp" || got["remote"].HTTPHeaders["Authorization"] != "stub" {
			t.Errorf("remote server not converted: %+v", got["remote"])
		}
		if got["local"].Command != "srv" {
			t.Errorf("local server not converted: %+v", got["local"])
		}
	})

	t.Run("name collision", func(t *testing.T) {
		_, err := buildMCPServers(provider.PrepareOpts{
			MCPServers:      map[string]provider.MCPServerConfig{"dup": {URL: "http://relay/mcp"}},
			LocalMCPServers: map[string]provider.LocalMCPServerConfig{"dup": {Command: "srv"}},
		})
		if err == nil || !contains(err.Error(), "must be unique") {
			t.Fatalf("expected a name-collision error, got %v", err)
		}
	})
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

// stagedConfig parses the config.toml PrepareContainer wrote, and asserts the
// staging dir has no mcp.json: Codex reads MCP servers from config.toml only,
// and a stale mcp.json would be copied into the workspace by moat-init.
func stagedConfig(t *testing.T, stagingDir string) Config {
	t.Helper()

	if _, err := os.Stat(filepath.Join(stagingDir, "mcp.json")); err == nil {
		t.Error("mcp.json should not be staged — Codex ignores it")
	}

	data, err := os.ReadFile(filepath.Join(stagingDir, "config.toml"))
	if err != nil {
		t.Fatalf("reading config.toml: %v", err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config.toml is not valid TOML: %v\n%s", err, data)
	}
	return cfg
}

func TestPrepareContainer_LocalMCP(t *testing.T) {
	p := &Provider{}

	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{
		ContainerHome: "/home/moatuser",
		LocalMCPServers: map[string]provider.LocalMCPServerConfig{
			"my-server": {
				Command: "/usr/local/bin/mcp-server",
				Args:    []string{"--verbose"},
				Env:     map[string]string{"DEBUG": "1"},
				Cwd:     "/workspace",
			},
		},
	})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()

	srv, ok := stagedConfig(t, cfg.StagingDir).MCPServers["my-server"]
	if !ok {
		t.Fatal("mcp_servers.my-server missing from config.toml")
	}
	if srv.Command != "/usr/local/bin/mcp-server" {
		t.Errorf("command = %q", srv.Command)
	}
	if len(srv.Args) != 1 || srv.Args[0] != "--verbose" {
		t.Errorf("args = %v", srv.Args)
	}
	if srv.Env["DEBUG"] != "1" {
		t.Errorf("env = %v", srv.Env)
	}
	if srv.Cwd != "/workspace" {
		t.Errorf("cwd = %q", srv.Cwd)
	}
}

func TestPrepareContainer_RemoteMCP(t *testing.T) {
	p := &Provider{}

	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{
		ContainerHome: "/home/moatuser",
		MCPServers: map[string]provider.MCPServerConfig{
			"linear": {
				URL:     "http://moat-proxy:8080/mcp/tok123/linear",
				Headers: map[string]string{"Authorization": "moat-stub-linear"},
			},
		},
	})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()

	srv, ok := stagedConfig(t, cfg.StagingDir).MCPServers["linear"]
	if !ok {
		t.Fatal("mcp_servers.linear missing from config.toml")
	}
	// The relay URL, not the server's real URL — the proxy injects credentials.
	if srv.URL != "http://moat-proxy:8080/mcp/tok123/linear" {
		t.Errorf("url = %q", srv.URL)
	}
	if srv.HTTPHeaders["Authorization"] != "moat-stub-linear" {
		t.Errorf("http_headers = %v", srv.HTTPHeaders)
	}
}

func TestPrepareContainer_NoMCP(t *testing.T) {
	p := &Provider{}

	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{
		ContainerHome: "/home/moatuser",
		// No MCP servers of either kind
	})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()

	if servers := stagedConfig(t, cfg.StagingDir).MCPServers; len(servers) != 0 {
		t.Errorf("expected no mcp_servers, got %v", servers)
	}
}

func TestPrepareContainer_MCP_MultipleServers(t *testing.T) {
	p := &Provider{}

	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{
		ContainerHome: "/home/moatuser",
		MCPServers: map[string]provider.MCPServerConfig{
			"remote": {URL: "http://moat-proxy:8080/mcp/tok/remote"},
		},
		LocalMCPServers: map[string]provider.LocalMCPServerConfig{
			"server-a": {
				Command: "mcp-a",
				Args:    []string{"--mode", "fast"},
			},
			"server-b": {
				Command: "mcp-b",
				Env:     map[string]string{"PORT": "3001"},
				Cwd:     "/opt/tools",
			},
		},
	})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()

	servers := stagedConfig(t, cfg.StagingDir).MCPServers
	if len(servers) != 3 {
		t.Fatalf("expected 3 mcp servers, got %v", servers)
	}
	if servers["server-a"].Command != "mcp-a" {
		t.Errorf("server-a = %+v", servers["server-a"])
	}
	if servers["server-b"].Command != "mcp-b" || servers["server-b"].Cwd != "/opt/tools" {
		t.Errorf("server-b = %+v", servers["server-b"])
	}
	if servers["remote"].URL != "http://moat-proxy:8080/mcp/tok/remote" {
		t.Errorf("remote = %+v", servers["remote"])
	}
}

func TestPrepareContainer_MCP_NameCollision(t *testing.T) {
	p := &Provider{}

	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{
		ContainerHome:   "/home/moatuser",
		MCPServers:      map[string]provider.MCPServerConfig{"dup": {URL: "http://relay/mcp"}},
		LocalMCPServers: map[string]provider.LocalMCPServerConfig{"dup": {Command: "srv"}},
	})
	if err == nil {
		cfg.Cleanup()
		t.Fatal("expected an error when a remote and local server share a name")
	}
	if !contains(err.Error(), "must be unique") {
		t.Errorf("error = %v, want a name-collision error", err)
	}
}

func TestPrepareContainer_LocalMCP_MinimalFields(t *testing.T) {
	p := &Provider{}

	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{
		ContainerHome: "/home/moatuser",
		LocalMCPServers: map[string]provider.LocalMCPServerConfig{
			"simple": {
				Command: "bare-mcp",
			},
		},
	})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()

	data, err := os.ReadFile(filepath.Join(cfg.StagingDir, "config.toml"))
	if err != nil {
		t.Fatalf("reading config.toml: %v", err)
	}

	content := string(data)
	if !contains(content, `command = 'bare-mcp'`) && !contains(content, `command = "bare-mcp"`) {
		t.Errorf("config.toml should contain the command, got: %s", content)
	}
	// Unset optional fields must be omitted rather than emitted empty.
	if contains(content, "env =") || contains(content, "[mcp_servers.simple.env]") {
		t.Errorf("config.toml should not contain env when not set, got: %s", content)
	}
	if contains(content, "cwd =") {
		t.Errorf("config.toml should not contain cwd when not set, got: %s", content)
	}
	if contains(content, "url =") {
		t.Errorf("config.toml should not contain url for a stdio server, got: %s", content)
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
