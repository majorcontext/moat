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

func TestLoadConfigRejectsRuntime(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
name: myapp
agent: test
runtime:
  node: "20"
`
	os.WriteFile(configPath, []byte(content), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load should error when runtime field is present")
	}
	if !strings.Contains(err.Error(), "no longer supported") {
		t.Errorf("error should mention 'no longer supported', got: %v", err)
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
