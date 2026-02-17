package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: claude-code
version: 1.0.46

dependencies:
  - node@20
  - python@3.11

grants:
  - github:repo
  - aws:s3.read

env:
  NODE_ENV: development
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agent != "claude-code" {
		t.Errorf("Agent = %q, want %q", cfg.Agent, "claude-code")
	}
	if cfg.Version != "1.0.46" {
		t.Errorf("Version = %q, want %q", cfg.Version, "1.0.46")
	}
	if len(cfg.Dependencies) != 2 {
		t.Errorf("Dependencies = %d, want 2", len(cfg.Dependencies))
	}
	if len(cfg.Grants) != 2 {
		t.Errorf("Grants = %d, want 2", len(cfg.Grants))
	}
	if cfg.Env["NODE_ENV"] != "development" {
		t.Errorf("Env[NODE_ENV] = %q, want %q", cfg.Env["NODE_ENV"], "development")
	}
}

func TestLoadConfigWithSSHGrants(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: my-agent

grants:
  - github
  - ssh:github.com
  - ssh:gitlab.com
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Grants) != 3 {
		t.Errorf("Grants = %d, want 3", len(cfg.Grants))
	}
	// Verify SSH grants are preserved with correct format
	expectedGrants := []string{"github", "ssh:github.com", "ssh:gitlab.com"}
	for i, expected := range expectedGrants {
		if cfg.Grants[i] != expected {
			t.Errorf("Grants[%d] = %q, want %q", i, cfg.Grants[i], expected)
		}
	}
}

func TestLoadConfigNotFound(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load should not error for missing config: %v", err)
	}
	if cfg != nil {
		t.Error("Expected nil config when agent.yaml doesn't exist")
	}
}

func TestLoadConfigWithMounts(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
mounts:
  - ./data:/data:ro
  - ./cache:/cache
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Mounts) != 2 {
		t.Fatalf("Mounts = %d, want 2", len(cfg.Mounts))
	}
	if cfg.Mounts[0] != "./data:/data:ro" {
		t.Errorf("Mounts[0] = %q, want %q", cfg.Mounts[0], "./data:/data:ro")
	}
}

func TestLoadConfigWithName(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
name: myapp
agent: test-agent
ports:
  web: 3000
  api: 8080
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "myapp" {
		t.Errorf("Name = %q, want %q", cfg.Name, "myapp")
	}
	if len(cfg.Ports) != 2 {
		t.Fatalf("Ports = %d, want 2", len(cfg.Ports))
	}
	if cfg.Ports["web"] != 3000 {
		t.Errorf("Ports[web] = %d, want 3000", cfg.Ports["web"])
	}
	if cfg.Ports["api"] != 8080 {
		t.Errorf("Ports[api] = %d, want 8080", cfg.Ports["api"])
	}
}

func TestLoadConfigWithDependencies(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
name: myapp
agent: test

dependencies:
  - node@20
  - typescript
  - protoc@25.1
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Dependencies) != 3 {
		t.Fatalf("Dependencies = %d, want 3", len(cfg.Dependencies))
	}
	if cfg.Dependencies[0] != "node@20" {
		t.Errorf("Dependencies[0] = %q, want %q", cfg.Dependencies[0], "node@20")
	}
}

func TestLoadConfigAcceptsRuntime(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
name: myapp
agent: test
runtime: docker
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load should accept runtime field, got error: %v", err)
	}
	if cfg.Runtime != "docker" {
		t.Errorf("Runtime = %q, want %q", cfg.Runtime, "docker")
	}
}

func TestLoadConfigRejectsInvalidRuntime(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
name: myapp
agent: test
runtime: invalid
`
	os.WriteFile(configPath, []byte(content), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load should error when runtime is invalid")
	}
	if !strings.Contains(err.Error(), "invalid runtime") {
		t.Errorf("error should mention 'invalid runtime', got: %v", err)
	}
}

func TestLoadConfigWithUnifiedContainer(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
runtime: docker
container:
  memory: 8192
  cpus: 4
  dns: ["1.1.1.1", "8.8.8.8"]
dependencies:
  - node@20
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Runtime != "docker" {
		t.Errorf("Runtime = %q, want %q", cfg.Runtime, "docker")
	}

	if cfg.Container.Memory != 8192 {
		t.Errorf("Container.Memory = %d, want %d", cfg.Container.Memory, 8192)
	}

	if cfg.Container.CPUs != 4 {
		t.Errorf("Container.CPUs = %d, want %d", cfg.Container.CPUs, 4)
	}

	if len(cfg.Container.DNS) != 2 {
		t.Fatalf("Container.DNS length = %d, want 2", len(cfg.Container.DNS))
	}

	if cfg.Container.DNS[0] != "1.1.1.1" {
		t.Errorf("Container.DNS[0] = %q, want %q", cfg.Container.DNS[0], "1.1.1.1")
	}

	if cfg.Container.DNS[1] != "8.8.8.8" {
		t.Errorf("Container.DNS[1] = %q, want %q", cfg.Container.DNS[1], "8.8.8.8")
	}
}

func TestLoadConfigRejectsNegativeMemory(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
container:
  memory: -1
`
	os.WriteFile(configPath, []byte(content), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load should error when memory is negative")
	}
	if !strings.Contains(err.Error(), "must be non-negative") {
		t.Errorf("error should mention 'must be non-negative', got: %v", err)
	}
}

func TestLoadConfigRejectsTooSmallMemory(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
container:
  memory: 64
`
	os.WriteFile(configPath, []byte(content), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load should error when memory is too small")
	}
	if !strings.Contains(err.Error(), "at least 128 MB") {
		t.Errorf("error should mention 'at least 128 MB', got: %v", err)
	}
}

func TestLoadConfigRejectsNegativeCPUs(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
container:
  cpus: -5
`
	os.WriteFile(configPath, []byte(content), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load should error when cpus is negative")
	}
	if !strings.Contains(err.Error(), "must be non-negative") {
		t.Errorf("error should mention 'must be non-negative', got: %v", err)
	}
}

func TestLoadConfigWithNetworkStrict(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
network:
  policy: strict
  allow:
    - "api.openai.com"
    - "*.amazonaws.com"
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Network.Policy != "strict" {
		t.Errorf("Network.Policy = %q, want %q", cfg.Network.Policy, "strict")
	}
	if len(cfg.Network.Allow) != 2 {
		t.Fatalf("Network.Allow = %d, want 2", len(cfg.Network.Allow))
	}
	if cfg.Network.Allow[0] != "api.openai.com" {
		t.Errorf("Network.Allow[0] = %q, want %q", cfg.Network.Allow[0], "api.openai.com")
	}
	if cfg.Network.Allow[1] != "*.amazonaws.com" {
		t.Errorf("Network.Allow[1] = %q, want %q", cfg.Network.Allow[1], "*.amazonaws.com")
	}
}

func TestLoadConfigWithNetworkPermissive(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
network:
  policy: permissive
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Network.Policy != "permissive" {
		t.Errorf("Network.Policy = %q, want %q", cfg.Network.Policy, "permissive")
	}
	if len(cfg.Network.Allow) != 0 {
		t.Errorf("Network.Allow = %d, want 0", len(cfg.Network.Allow))
	}
}

func TestLoadConfigNetworkDefaultsToPermissive(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Network.Policy != "permissive" {
		t.Errorf("Network.Policy = %q, want %q (default)", cfg.Network.Policy, "permissive")
	}
	if len(cfg.Network.Allow) != 0 {
		t.Errorf("Network.Allow = %d, want 0 (default)", len(cfg.Network.Allow))
	}
}

func TestLoadConfigWithNetworkAllowOnly(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
network:
  allow:
    - "example.com"
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Policy should default to permissive even if allow is specified
	if cfg.Network.Policy != "permissive" {
		t.Errorf("Network.Policy = %q, want %q (default)", cfg.Network.Policy, "permissive")
	}
	if len(cfg.Network.Allow) != 1 {
		t.Fatalf("Network.Allow = %d, want 1", len(cfg.Network.Allow))
	}
	if cfg.Network.Allow[0] != "example.com" {
		t.Errorf("Network.Allow[0] = %q, want %q", cfg.Network.Allow[0], "example.com")
	}
}

func TestLoadConfigRejectsInvalidNetworkPolicy(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
network:
  policy: invalid
`
	os.WriteFile(configPath, []byte(content), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load should error on invalid network policy")
	}
	if !strings.Contains(err.Error(), "invalid network policy") {
		t.Errorf("error should mention 'invalid network policy', got: %v", err)
	}
}

func TestLoadConfigRejectsInvalidSandbox(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	// Test invalid sandbox value
	content := `
agent: test
sandbox: disabled
`
	os.WriteFile(configPath, []byte(content), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load should error on invalid sandbox value")
	}
	if !strings.Contains(err.Error(), "invalid sandbox value") {
		t.Errorf("error should mention 'invalid sandbox value', got: %v", err)
	}
}

func TestLoadConfigAcceptsSandboxNone(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
sandbox: none
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load should accept sandbox: none, got error: %v", err)
	}
	if cfg.Sandbox != "none" {
		t.Errorf("Sandbox = %q, want %q", cfg.Sandbox, "none")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("DefaultConfig() returned nil")
	}
	if cfg.Env == nil {
		t.Error("DefaultConfig() should initialize Env map")
	}
	if cfg.Network.Policy != "permissive" {
		t.Errorf("DefaultConfig() Network.Policy = %q, want %q", cfg.Network.Policy, "permissive")
	}
	if len(cfg.Network.Allow) != 0 {
		t.Errorf("DefaultConfig() Network.Allow = %d, want 0", len(cfg.Network.Allow))
	}
}

func TestLoad_Secrets(t *testing.T) {
	dir := t.TempDir()
	content := `
agent: claude
secrets:
  OPENAI_API_KEY: op://Dev/OpenAI/api-key
  DATABASE_URL: op://Prod/Database/url
`
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Secrets) != 2 {
		t.Errorf("expected 2 secrets, got %d", len(cfg.Secrets))
	}
	if cfg.Secrets["OPENAI_API_KEY"] != "op://Dev/OpenAI/api-key" {
		t.Errorf("unexpected OPENAI_API_KEY: %s", cfg.Secrets["OPENAI_API_KEY"])
	}
	if cfg.Secrets["DATABASE_URL"] != "op://Prod/Database/url" {
		t.Errorf("unexpected DATABASE_URL: %s", cfg.Secrets["DATABASE_URL"])
	}
}

func TestLoad_SecretsEnvOverlap(t *testing.T) {
	dir := t.TempDir()
	content := `
agent: claude
env:
  API_KEY: literal-value
secrets:
  API_KEY: op://Dev/Key/value
`
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for overlapping env/secrets keys")
	}
	if !strings.Contains(err.Error(), "API_KEY") {
		t.Errorf("error should mention the overlapping key: %v", err)
	}
}

func TestLoad_SecretsInvalidReference(t *testing.T) {
	dir := t.TempDir()
	content := `
agent: claude
secrets:
  API_KEY: not-a-valid-uri
`
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for invalid secret reference")
	}
	if !strings.Contains(err.Error(), "missing scheme") {
		t.Errorf("error should mention missing scheme: %v", err)
	}
}

func TestLoadConfigWithCommand(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
command: ["npm", "start"]
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Command) != 2 {
		t.Fatalf("Command = %d args, want 2", len(cfg.Command))
	}
	if cfg.Command[0] != "npm" {
		t.Errorf("Command[0] = %q, want %q", cfg.Command[0], "npm")
	}
	if cfg.Command[1] != "start" {
		t.Errorf("Command[1] = %q, want %q", cfg.Command[1], "start")
	}
}

func TestLoadConfigWithCommandShell(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
command: ["sh", "-c", "echo hello && npm test"]
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Command) != 3 {
		t.Fatalf("Command = %d args, want 3", len(cfg.Command))
	}
	if cfg.Command[0] != "sh" {
		t.Errorf("Command[0] = %q, want %q", cfg.Command[0], "sh")
	}
	if cfg.Command[2] != "echo hello && npm test" {
		t.Errorf("Command[2] = %q, want %q", cfg.Command[2], "echo hello && npm test")
	}
}

func TestLoadConfigWithEmptyCommand(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
command: ["", "arg1"]
`
	os.WriteFile(configPath, []byte(content), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load should error when command[0] is empty")
	}
	if !strings.Contains(err.Error(), "command[0] cannot be empty") {
		t.Errorf("error should mention empty command: %v", err)
	}
}

func TestShouldSyncClaudeLogs(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name     string
		config   Config
		expected bool
	}{
		{
			name:     "default without anthropic grant",
			config:   Config{Grants: []string{"github"}},
			expected: false,
		},
		{
			name:     "default with anthropic grant",
			config:   Config{Grants: []string{"anthropic"}},
			expected: true,
		},
		{
			name:     "default with anthropic:scope grant",
			config:   Config{Grants: []string{"anthropic:admin"}},
			expected: true,
		},
		{
			name:     "explicit true without anthropic",
			config:   Config{Claude: ClaudeConfig{SyncLogs: boolPtr(true)}},
			expected: true,
		},
		{
			name:     "explicit false with anthropic",
			config:   Config{Grants: []string{"anthropic"}, Claude: ClaudeConfig{SyncLogs: boolPtr(false)}},
			expected: false,
		},
		{
			name:     "explicit true with anthropic",
			config:   Config{Grants: []string{"anthropic"}, Claude: ClaudeConfig{SyncLogs: boolPtr(true)}},
			expected: true,
		},
		{
			name:     "empty config",
			config:   Config{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.ShouldSyncClaudeLogs()
			if result != tt.expected {
				t.Errorf("ShouldSyncClaudeLogs() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestLoadConfigWithClaudeSyncLogs(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
claude:
  sync_logs: true
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Claude.SyncLogs == nil {
		t.Fatal("Claude.SyncLogs should not be nil")
	}
	if *cfg.Claude.SyncLogs != true {
		t.Errorf("Claude.SyncLogs = %v, want true", *cfg.Claude.SyncLogs)
	}
}

func TestLoadConfigWithInteractive(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
command: ["bash"]
interactive: true
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Interactive {
		t.Error("Interactive should be true")
	}
}

func TestLoadConfigInteractiveDefaultFalse(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
command: ["npm", "start"]
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Interactive {
		t.Error("Interactive should default to false")
	}
}

func TestLoadConfigWithClaudePlugins(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
claude:
  plugins:
    typescript-lsp@official: true
    debug-tool@acme: false
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Claude.Plugins) != 2 {
		t.Fatalf("Claude.Plugins = %d, want 2", len(cfg.Claude.Plugins))
	}
	if !cfg.Claude.Plugins["typescript-lsp@official"] {
		t.Error("typescript-lsp@official should be enabled")
	}
	if cfg.Claude.Plugins["debug-tool@acme"] {
		t.Error("debug-tool@acme should be disabled")
	}
}

func TestLoadConfigWithClaudeMarketplaces(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
claude:
  marketplaces:
    acme:
      source: github
      repo: acme-corp/claude-plugins
    internal:
      source: git
      url: git@github.com:org/internal-plugins.git
      ref: main
    local:
      source: directory
      path: /opt/plugins
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Claude.Marketplaces) != 3 {
		t.Fatalf("Claude.Marketplaces = %d, want 3", len(cfg.Claude.Marketplaces))
	}

	acme := cfg.Claude.Marketplaces["acme"]
	if acme.Source != "github" {
		t.Errorf("acme.Source = %q, want %q", acme.Source, "github")
	}
	if acme.Repo != "acme-corp/claude-plugins" {
		t.Errorf("acme.Repo = %q, want %q", acme.Repo, "acme-corp/claude-plugins")
	}

	internal := cfg.Claude.Marketplaces["internal"]
	if internal.Source != "git" {
		t.Errorf("internal.Source = %q, want %q", internal.Source, "git")
	}
	if internal.URL != "git@github.com:org/internal-plugins.git" {
		t.Errorf("internal.URL = %q, want %q", internal.URL, "git@github.com:org/internal-plugins.git")
	}
	if internal.Ref != "main" {
		t.Errorf("internal.Ref = %q, want %q", internal.Ref, "main")
	}

	local := cfg.Claude.Marketplaces["local"]
	if local.Source != "directory" {
		t.Errorf("local.Source = %q, want %q", local.Source, "directory")
	}
	if local.Path != "/opt/plugins" {
		t.Errorf("local.Path = %q, want %q", local.Path, "/opt/plugins")
	}
}

func TestLoadConfigMarketplaceValidation(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		errContains string
	}{
		{
			name: "missing source",
			content: `
agent: test
claude:
  marketplaces:
    bad:
      repo: owner/repo
`,
			errContains: "'source' is required",
		},
		{
			name: "invalid source",
			content: `
agent: test
claude:
  marketplaces:
    bad:
      source: invalid
`,
			errContains: "invalid source",
		},
		{
			name: "github missing repo",
			content: `
agent: test
claude:
  marketplaces:
    bad:
      source: github
`,
			errContains: "'repo' is required",
		},
		{
			name: "github invalid repo format",
			content: `
agent: test
claude:
  marketplaces:
    bad:
      source: github
      repo: just-name
`,
			errContains: "owner/repo format",
		},
		{
			name: "git missing url",
			content: `
agent: test
claude:
  marketplaces:
    bad:
      source: git
`,
			errContains: "'url' is required",
		},
		{
			name: "directory missing path",
			content: `
agent: test
claude:
  marketplaces:
    bad:
      source: directory
`,
			errContains: "'path' is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "agent.yaml")
			os.WriteFile(configPath, []byte(tt.content), 0644)

			_, err := Load(dir)
			if err == nil {
				t.Fatal("Load should error")
			}
			if !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("error should contain %q, got: %v", tt.errContains, err)
			}
		})
	}
}

func TestLoadConfigWithClaudeMCP(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
claude:
  mcp:
    github:
      command: npx
      args: ["-y", "@modelcontextprotocol/server-github"]
      grant: github
    filesystem:
      command: npx
      args: ["-y", "@anthropic/mcp-server-filesystem", "/workspace"]
      cwd: /workspace
    custom:
      command: python
      args: ["-m", "my_server"]
      env:
        API_URL: https://api.example.com
        TOKEN: "${secrets.MY_TOKEN}"
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Claude.MCP) != 3 {
		t.Fatalf("Claude.MCP = %d, want 3", len(cfg.Claude.MCP))
	}

	github := cfg.Claude.MCP["github"]
	if github.Command != "npx" {
		t.Errorf("github.Command = %q, want %q", github.Command, "npx")
	}
	if len(github.Args) != 2 {
		t.Errorf("github.Args = %d, want 2", len(github.Args))
	}
	if github.Grant != "github" {
		t.Errorf("github.Grant = %q, want %q", github.Grant, "github")
	}

	filesystem := cfg.Claude.MCP["filesystem"]
	if filesystem.Cwd != "/workspace" {
		t.Errorf("filesystem.Cwd = %q, want %q", filesystem.Cwd, "/workspace")
	}

	custom := cfg.Claude.MCP["custom"]
	if custom.Env["API_URL"] != "https://api.example.com" {
		t.Errorf("custom.Env[API_URL] = %q, want %q", custom.Env["API_URL"], "https://api.example.com")
	}
	if custom.Env["TOKEN"] != "${secrets.MY_TOKEN}" {
		t.Errorf("custom.Env[TOKEN] = %q, want %q", custom.Env["TOKEN"], "${secrets.MY_TOKEN}")
	}
}

func TestLoadConfigMCPMissingCommand(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
claude:
  mcp:
    bad:
      args: ["--help"]
`
	os.WriteFile(configPath, []byte(content), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load should error when MCP command is missing")
	}
	if !strings.Contains(err.Error(), "'command' is required") {
		t.Errorf("error should mention missing command: %v", err)
	}
}

func TestSnapshotConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test-agent
snapshots:
  disabled: false
  triggers:
    disable_pre_run: false
    disable_git_commits: true
    disable_builds: false
    disable_idle: false
    idle_threshold_seconds: 60
  exclude:
    ignore_gitignore: false
    additional:
      - "secrets/"
      - ".env.local"
  retention:
    max_count: 5
    delete_initial: false
tracing:
  disable_exec: false
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Verify snapshot config
	if cfg.Snapshots.Disabled {
		t.Error("Snapshots.Disabled should be false")
	}

	// Verify triggers
	if cfg.Snapshots.Triggers.DisablePreRun {
		t.Error("Snapshots.Triggers.DisablePreRun should be false")
	}
	if !cfg.Snapshots.Triggers.DisableGitCommits {
		t.Error("Snapshots.Triggers.DisableGitCommits should be true")
	}
	if cfg.Snapshots.Triggers.DisableBuilds {
		t.Error("Snapshots.Triggers.DisableBuilds should be false")
	}
	if cfg.Snapshots.Triggers.DisableIdle {
		t.Error("Snapshots.Triggers.DisableIdle should be false")
	}
	if cfg.Snapshots.Triggers.IdleThresholdSeconds != 60 {
		t.Errorf("Snapshots.Triggers.IdleThresholdSeconds = %d, want 60", cfg.Snapshots.Triggers.IdleThresholdSeconds)
	}

	// Verify exclude
	if cfg.Snapshots.Exclude.IgnoreGitignore {
		t.Error("Snapshots.Exclude.IgnoreGitignore should be false")
	}
	if len(cfg.Snapshots.Exclude.Additional) != 2 {
		t.Fatalf("Snapshots.Exclude.Additional = %d, want 2", len(cfg.Snapshots.Exclude.Additional))
	}
	if cfg.Snapshots.Exclude.Additional[0] != "secrets/" {
		t.Errorf("Snapshots.Exclude.Additional[0] = %q, want %q", cfg.Snapshots.Exclude.Additional[0], "secrets/")
	}
	if cfg.Snapshots.Exclude.Additional[1] != ".env.local" {
		t.Errorf("Snapshots.Exclude.Additional[1] = %q, want %q", cfg.Snapshots.Exclude.Additional[1], ".env.local")
	}

	// Verify retention
	if cfg.Snapshots.Retention.MaxCount != 5 {
		t.Errorf("Snapshots.Retention.MaxCount = %d, want 5", cfg.Snapshots.Retention.MaxCount)
	}
	if cfg.Snapshots.Retention.DeleteInitial {
		t.Error("Snapshots.Retention.DeleteInitial should be false")
	}

	// Verify tracing
	if cfg.Tracing.DisableExec {
		t.Error("Tracing.DisableExec should be false")
	}
}

func TestSnapshotConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test-agent
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Verify snapshot defaults
	if cfg.Snapshots.Disabled {
		t.Error("Snapshots.Disabled should default to false")
	}
	if cfg.Snapshots.Triggers.IdleThresholdSeconds != 30 {
		t.Errorf("Snapshots.Triggers.IdleThresholdSeconds = %d, want 30 (default)", cfg.Snapshots.Triggers.IdleThresholdSeconds)
	}
	if cfg.Snapshots.Retention.MaxCount != 10 {
		t.Errorf("Snapshots.Retention.MaxCount = %d, want 10 (default)", cfg.Snapshots.Retention.MaxCount)
	}

	// Verify other snapshot defaults are false/empty
	if cfg.Snapshots.Triggers.DisablePreRun {
		t.Error("Snapshots.Triggers.DisablePreRun should default to false")
	}
	if cfg.Snapshots.Triggers.DisableGitCommits {
		t.Error("Snapshots.Triggers.DisableGitCommits should default to false")
	}
	if cfg.Snapshots.Triggers.DisableBuilds {
		t.Error("Snapshots.Triggers.DisableBuilds should default to false")
	}
	if cfg.Snapshots.Triggers.DisableIdle {
		t.Error("Snapshots.Triggers.DisableIdle should default to false")
	}
	if cfg.Snapshots.Exclude.IgnoreGitignore {
		t.Error("Snapshots.Exclude.IgnoreGitignore should default to false")
	}
	if len(cfg.Snapshots.Exclude.Additional) != 0 {
		t.Errorf("Snapshots.Exclude.Additional = %d, want 0 (default)", len(cfg.Snapshots.Exclude.Additional))
	}
	if cfg.Snapshots.Retention.DeleteInitial {
		t.Error("Snapshots.Retention.DeleteInitial should default to false")
	}

	// Verify tracing defaults
	if cfg.Tracing.DisableExec {
		t.Error("Tracing.DisableExec should default to false")
	}
}

func TestDefaultConfigSnapshotDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Snapshots.Triggers.IdleThresholdSeconds != 30 {
		t.Errorf("DefaultConfig() Snapshots.Triggers.IdleThresholdSeconds = %d, want 30", cfg.Snapshots.Triggers.IdleThresholdSeconds)
	}
	if cfg.Snapshots.Retention.MaxCount != 10 {
		t.Errorf("DefaultConfig() Snapshots.Retention.MaxCount = %d, want 10", cfg.Snapshots.Retention.MaxCount)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
}

func TestLoad_MCPServers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "agent.yaml", `
mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7
      header: CONTEXT7_API_KEY
  - name: public-mcp
    url: https://public.example.com/mcp
`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(cfg.MCP) != 2 {
		t.Fatalf("expected 2 MCP servers, got %d", len(cfg.MCP))
	}

	// Check first server (with auth)
	ctx7 := cfg.MCP[0]
	if ctx7.Name != "context7" {
		t.Errorf("expected name 'context7', got %q", ctx7.Name)
	}
	if ctx7.URL != "https://mcp.context7.com/mcp" {
		t.Errorf("expected URL 'https://mcp.context7.com/mcp', got %q", ctx7.URL)
	}
	if ctx7.Auth == nil {
		t.Fatal("expected auth to be set")
	}
	if ctx7.Auth.Grant != "mcp-context7" {
		t.Errorf("expected grant 'mcp-context7', got %q", ctx7.Auth.Grant)
	}
	if ctx7.Auth.Header != "CONTEXT7_API_KEY" {
		t.Errorf("expected header 'CONTEXT7_API_KEY', got %q", ctx7.Auth.Header)
	}

	// Check second server (no auth)
	public := cfg.MCP[1]
	if public.Name != "public-mcp" {
		t.Errorf("expected name 'public-mcp', got %q", public.Name)
	}
	if public.Auth != nil {
		t.Errorf("expected auth to be nil, got %+v", public.Auth)
	}
}

func TestLoad_MCP_Validation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "missing name",
			yaml: `
mcp:
  - url: https://example.com
    auth:
      grant: mcp-test
      header: API_KEY
`,
			wantErr: "mcp[0]: 'name' is required",
		},
		{
			name: "missing url",
			yaml: `
mcp:
  - name: test
    auth:
      grant: mcp-test
      header: API_KEY
`,
			wantErr: "mcp[0]: 'url' is required",
		},
		{
			name: "non-https url",
			yaml: `
mcp:
  - name: test
    url: http://example.com
`,
			wantErr: "mcp[0]: 'url' must use HTTPS",
		},
		{
			name: "auth missing grant",
			yaml: `
mcp:
  - name: test
    url: https://example.com
    auth:
      header: API_KEY
`,
			wantErr: "mcp[0]: 'auth.grant' is required when auth is specified",
		},
		{
			name: "auth missing header",
			yaml: `
mcp:
  - name: test
    url: https://example.com
    auth:
      grant: mcp-test
`,
			wantErr: "mcp[0]: 'auth.header' is required when auth is specified",
		},
		{
			name: "duplicate names",
			yaml: `
mcp:
  - name: test
    url: https://example.com
  - name: test
    url: https://other.com
`,
			wantErr: "mcp[1]: duplicate name 'test'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "agent.yaml", tt.yaml)

			_, err := Load(dir)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestLoad_Proxies_Valid(t *testing.T) {
	dir := t.TempDir()
	content := `
agent: claude-code
proxies:
  - name: squid
    image: squid:latest
    port: 3128
  - name: tinyproxy
    image: tinyproxy:latest
    port: 8888
`
	os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Proxies) != 2 {
		t.Fatalf("Proxies = %d, want 2", len(cfg.Proxies))
	}
	if cfg.Proxies[0].Name != "squid" {
		t.Errorf("Proxies[0].Name = %q, want %q", cfg.Proxies[0].Name, "squid")
	}
	if cfg.Proxies[0].Image != "squid:latest" {
		t.Errorf("Proxies[0].Image = %q, want %q", cfg.Proxies[0].Image, "squid:latest")
	}
	if cfg.Proxies[0].Port != 3128 {
		t.Errorf("Proxies[0].Port = %d, want %d", cfg.Proxies[0].Port, 3128)
	}
	if cfg.Proxies[1].Name != "tinyproxy" {
		t.Errorf("Proxies[1].Name = %q, want %q", cfg.Proxies[1].Name, "tinyproxy")
	}
}

func TestLoad_Proxies_WithEnv(t *testing.T) {
	dir := t.TempDir()
	content := `
agent: claude-code
proxies:
  - name: squid
    image: squid:latest
    port: 3128
    env:
      SQUID_MAX_CACHE: "1024"
      DEBUG: "true"
`
	os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Proxies[0].Env["SQUID_MAX_CACHE"] != "1024" {
		t.Errorf("Env[SQUID_MAX_CACHE] = %q, want %q", cfg.Proxies[0].Env["SQUID_MAX_CACHE"], "1024")
	}
	if cfg.Proxies[0].Env["DEBUG"] != "true" {
		t.Errorf("Env[DEBUG] = %q, want %q", cfg.Proxies[0].Env["DEBUG"], "true")
	}
}

func TestLoad_Proxies_MissingName(t *testing.T) {
	dir := t.TempDir()
	content := `
agent: claude-code
proxies:
  - image: squid:latest
    port: 3128
`
	os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(content), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for missing proxy name")
	}
	if !strings.Contains(err.Error(), "'name' is required") {
		t.Errorf("error = %q, want to contain 'name' is required", err.Error())
	}
}

func TestLoad_Proxies_MissingImage(t *testing.T) {
	dir := t.TempDir()
	content := `
agent: claude-code
proxies:
  - name: squid
    port: 3128
`
	os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(content), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for missing proxy image")
	}
	if !strings.Contains(err.Error(), "'image' is required") {
		t.Errorf("error = %q, want to contain 'image' is required", err.Error())
	}
}

func TestLoad_Proxies_MissingPort(t *testing.T) {
	dir := t.TempDir()
	content := `
agent: claude-code
proxies:
  - name: squid
    image: squid:latest
`
	os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(content), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for missing proxy port")
	}
	if !strings.Contains(err.Error(), "'port' must be a positive integer") {
		t.Errorf("error = %q, want to contain 'port' must be a positive integer", err.Error())
	}
}

func TestLoad_Proxies_NegativePort(t *testing.T) {
	dir := t.TempDir()
	content := `
agent: claude-code
proxies:
  - name: squid
    image: squid:latest
    port: -1
`
	os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(content), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for negative port")
	}
	if !strings.Contains(err.Error(), "'port' must be a positive integer") {
		t.Errorf("error = %q, want to contain 'port' must be a positive integer", err.Error())
	}
}

func TestLoad_Proxies_DuplicateName(t *testing.T) {
	dir := t.TempDir()
	content := `
agent: claude-code
proxies:
  - name: squid
    image: squid:latest
    port: 3128
  - name: squid
    image: squid2:latest
    port: 3129
`
	os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(content), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for duplicate proxy name")
	}
	if !strings.Contains(err.Error(), "duplicate name") {
		t.Errorf("error = %q, want to contain 'duplicate name'", err.Error())
	}
}

func TestLoad_Proxies_EmptyList(t *testing.T) {
	dir := t.TempDir()
	content := `
agent: claude-code
proxies: []
`
	os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Proxies) != 0 {
		t.Errorf("Proxies = %d, want 0", len(cfg.Proxies))
	}
}

func TestLoad_Proxies_SingleProxy(t *testing.T) {
	dir := t.TempDir()
	content := `
agent: claude-code
proxies:
  - name: corporate
    image: corp-proxy:latest
    port: 8080
`
	os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Proxies) != 1 {
		t.Fatalf("Proxies = %d, want 1", len(cfg.Proxies))
	}
	if cfg.Proxies[0].Name != "corporate" {
		t.Errorf("Name = %q, want %q", cfg.Proxies[0].Name, "corporate")
	}
}

func TestLoad_Proxies_NoEnvField(t *testing.T) {
	dir := t.TempDir()
	content := `
agent: claude-code
proxies:
  - name: simple
    image: simple:latest
    port: 3128
`
	os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Proxies[0].Env) != 0 {
		t.Errorf("Env = %v, want nil or empty", cfg.Proxies[0].Env)
	}
}

func TestLoadConfigWithHooks(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
hooks:
  post_build: git config --global core.autocrlf input
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Hooks.PostBuild != "git config --global core.autocrlf input" {
		t.Errorf("Hooks.PostBuild = %q, want %q", cfg.Hooks.PostBuild, "git config --global core.autocrlf input")
	}
}

func TestLoadConfigWithHooksAll(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
hooks:
  post_build: git config --global core.autocrlf input
  post_build_root: apt-get install -y figlet
  pre_run: npm install
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Hooks.PostBuild != "git config --global core.autocrlf input" {
		t.Errorf("Hooks.PostBuild = %q, want %q", cfg.Hooks.PostBuild, "git config --global core.autocrlf input")
	}
	if cfg.Hooks.PostBuildRoot != "apt-get install -y figlet" {
		t.Errorf("Hooks.PostBuildRoot = %q, want %q", cfg.Hooks.PostBuildRoot, "apt-get install -y figlet")
	}
	if cfg.Hooks.PreRun != "npm install" {
		t.Errorf("Hooks.PreRun = %q, want %q", cfg.Hooks.PreRun, "npm install")
	}
}

func TestLoadConfigWithHooksEmpty(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
agent: test
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Hooks.PostBuild != "" {
		t.Errorf("Hooks.PostBuild should be empty, got %q", cfg.Hooks.PostBuild)
	}
	if cfg.Hooks.PostBuildRoot != "" {
		t.Errorf("Hooks.PostBuildRoot should be empty, got %q", cfg.Hooks.PostBuildRoot)
	}
	if cfg.Hooks.PreRun != "" {
		t.Errorf("Hooks.PreRun should be empty, got %q", cfg.Hooks.PreRun)
	}
}

func TestServicesValidation(t *testing.T) {
	cfg := &Config{
		Services: map[string]ServiceSpec{
			"postgres": {},
		},
	}
	err := cfg.ValidateServices([]string{"node"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "postgres not declared in dependencies") {
		t.Errorf("expected error to contain 'postgres not declared in dependencies', got %q", err.Error())
	}
}

func TestServicesValidationPass(t *testing.T) {
	cfg := &Config{
		Services: map[string]ServiceSpec{
			"postgres": {
				Env: map[string]string{"POSTGRES_DB": "myapp"},
			},
		},
	}
	err := cfg.ValidateServices([]string{"postgres"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestServiceWaitDefault(t *testing.T) {
	s := ServiceSpec{}
	if !s.ServiceWait() {
		t.Error("expected ServiceWait() to return true by default")
	}

	f := false
	s2 := ServiceSpec{Wait: &f}
	if s2.ServiceWait() {
		t.Error("expected ServiceWait() to return false when Wait is false")
	}
}
