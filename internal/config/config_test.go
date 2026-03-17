package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
		t.Error("Expected nil config when moat.yaml doesn't exist")
	}
}

func TestLoadConfigWithMounts(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "moat.yaml")

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
	if cfg.Mounts[0].Source != "./data" || cfg.Mounts[0].Target != "/data" || !cfg.Mounts[0].ReadOnly {
		t.Errorf("Mounts[0] = %+v, want source=./data target=/data ro=true", cfg.Mounts[0])
	}
}

func TestLoadConfigWithMountExcludes(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "moat.yaml")

	content := `
agent: claude-code

mounts:
  - ./data:/data:ro
  - source: .
    target: /workspace
    exclude:
      - node_modules
      - .venv
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Mounts) != 2 {
		t.Fatalf("Mounts = %d, want 2", len(cfg.Mounts))
	}

	// First mount: string form
	if cfg.Mounts[0].Source != "./data" {
		t.Errorf("Mounts[0].Source = %q, want %q", cfg.Mounts[0].Source, "./data")
	}
	if !cfg.Mounts[0].ReadOnly {
		t.Error("Mounts[0].ReadOnly = false, want true")
	}

	// Second mount: object form with excludes
	if cfg.Mounts[1].Source != "." {
		t.Errorf("Mounts[1].Source = %q, want %q", cfg.Mounts[1].Source, ".")
	}
	if cfg.Mounts[1].Target != "/workspace" {
		t.Errorf("Mounts[1].Target = %q, want %q", cfg.Mounts[1].Target, "/workspace")
	}
	if len(cfg.Mounts[1].Exclude) != 2 {
		t.Fatalf("Mounts[1].Exclude = %d, want 2", len(cfg.Mounts[1].Exclude))
	}
	if cfg.Mounts[1].Exclude[0] != "node_modules" {
		t.Errorf("Mounts[1].Exclude[0] = %q, want %q", cfg.Mounts[1].Exclude[0], "node_modules")
	}
}

func TestLoadConfigMountExcludeValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "rejects absolute exclude path",
			yaml: `
agent: test
mounts:
  - source: .
    target: /workspace
    exclude:
      - /tmp/foo
`,
			wantErr: "must be relative",
		},
		{
			name: "rejects dotdot exclude path",
			yaml: `
agent: test
mounts:
  - source: .
    target: /workspace
    exclude:
      - ../foo
`,
			wantErr: "must not contain '..'",
		},
		{
			name: "rejects duplicate exclude paths",
			yaml: `
agent: test
mounts:
  - source: .
    target: /workspace
    exclude:
      - node_modules
      - node_modules
`,
			wantErr: "duplicate exclude",
		},
		{
			name: "rejects duplicate mount targets",
			yaml: `
agent: test
mounts:
  - source: .
    target: /workspace
  - source: ./other
    target: /workspace
`,
			wantErr: "duplicate mount target",
		},
		{
			name: "rejects invalid mode",
			yaml: `
agent: test
mounts:
  - source: .
    target: /workspace
    mode: readonly
`,
			wantErr: "invalid mode",
		},
		{
			name: "rejects volume/exclude conflict",
			yaml: `
name: myagent
mounts:
  - source: .
    target: /workspace
    exclude:
      - node_modules
volumes:
  - name: deps
    target: /workspace/node_modules
`,
			wantErr: "conflicts with volume target",
		},
		{
			name: "rejects volume nested under exclude",
			yaml: `
name: myagent
mounts:
  - source: .
    target: /workspace
    exclude:
      - node_modules
volumes:
  - name: cache
    target: /workspace/node_modules/cache
`,
			wantErr: "conflicts with volume target",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(tt.yaml), 0644)

			_, err := Load(dir)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoadConfigWithName(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

	content := `
agent: test
network:
  policy: strict
  rules:
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
	if len(cfg.Network.Rules) != 2 {
		t.Fatalf("Network.Rules = %d, want 2", len(cfg.Network.Rules))
	}
	if cfg.Network.Rules[0].Host != "api.openai.com" {
		t.Errorf("Network.Rules[0].Host = %q, want %q", cfg.Network.Rules[0].Host, "api.openai.com")
	}
	if cfg.Network.Rules[1].Host != "*.amazonaws.com" {
		t.Errorf("Network.Rules[1].Host = %q, want %q", cfg.Network.Rules[1].Host, "*.amazonaws.com")
	}
}

func TestLoadConfigWithNetworkPermissive(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "moat.yaml")

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
	if len(cfg.Network.Rules) != 0 {
		t.Errorf("Network.Rules = %d, want 0", len(cfg.Network.Rules))
	}
}

func TestLoadConfigNetworkDefaultsToPermissive(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "moat.yaml")

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
	if len(cfg.Network.Rules) != 0 {
		t.Errorf("Network.Rules = %d, want 0 (default)", len(cfg.Network.Rules))
	}
}

func TestLoadConfigWithNetworkAllowOnly(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "moat.yaml")

	content := `
agent: test
network:
  allow:
    - "example.com"
`
	os.WriteFile(configPath, []byte(content), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load should error on deprecated network.allow field")
	}
	if !strings.Contains(err.Error(), "network.allow") {
		t.Errorf("error should mention network.allow, got: %v", err)
	}
}

func TestLoadConfigRejectsInvalidNetworkPolicy(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "moat.yaml")

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

func TestNetworkRulesConfig(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantN   int
		wantErr string
	}{
		{
			name:  "plain host",
			yaml:  "network:\n  policy: strict\n  rules:\n    - \"api.github.com\"\n",
			wantN: 1,
		},
		{
			name:  "host with rules",
			yaml:  "network:\n  policy: strict\n  rules:\n    - \"api.github.com\":\n        - \"allow GET /repos/*\"\n",
			wantN: 1,
		},
		{
			name:  "mixed",
			yaml:  "network:\n  policy: strict\n  rules:\n    - \"npmjs.org\"\n    - \"api.github.com\":\n        - \"allow GET /repos/*\"\n",
			wantN: 2,
		},
		{
			name:    "old allow field errors",
			yaml:    "network:\n  policy: strict\n  allow:\n    - \"api.github.com\"\n",
			wantErr: "network.allow",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(tt.yaml), 0644)
			cfg, err := Load(dir)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got: %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(cfg.Network.Rules) != tt.wantN {
				t.Errorf("got %d rules, want %d", len(cfg.Network.Rules), tt.wantN)
			}
		})
	}
}

func TestLoadConfigRejectsInvalidSandbox(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
	if len(cfg.Network.Rules) != 0 {
		t.Errorf("DefaultConfig() Network.Rules = %d, want 0", len(cfg.Network.Rules))
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
	if err := os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(content), 0644); err != nil {
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
	if err := os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(content), 0644); err != nil {
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
	if err := os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(content), 0644); err != nil {
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
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
			configPath := filepath.Join(dir, "moat.yaml")
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
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
	writeFile(t, dir, "moat.yaml", `
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
			writeFile(t, dir, "moat.yaml", tt.yaml)

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

func TestLoad_MCP_HostLocal(t *testing.T) {
	// Host-local MCP servers (localhost/127.0.0.1) should be allowed with http://
	dir := t.TempDir()
	writeFile(t, dir, "moat.yaml", `
mcp:
  - name: local-server
    url: http://localhost:3000/mcp
  - name: local-ip
    url: http://127.0.0.1:8080/sse
  - name: remote-server
    url: https://mcp.example.com/mcp
`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(cfg.MCP) != 3 {
		t.Fatalf("expected 3 MCP servers, got %d", len(cfg.MCP))
	}

	local := cfg.MCP[0]
	if local.Name != "local-server" {
		t.Errorf("expected name 'local-server', got %q", local.Name)
	}
	if local.URL != "http://localhost:3000/mcp" {
		t.Errorf("expected URL 'http://localhost:3000/mcp', got %q", local.URL)
	}

	localIP := cfg.MCP[1]
	if localIP.URL != "http://127.0.0.1:8080/sse" {
		t.Errorf("expected URL 'http://127.0.0.1:8080/sse', got %q", localIP.URL)
	}
}

func TestLoad_MCP_HostLocal_Validation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "http non-localhost rejected",
			yaml: `
mcp:
  - name: test
    url: http://example.com/mcp
`,
			wantErr: "mcp[0]: 'url' must use HTTPS (http:// is only allowed for localhost, 127.0.0.1, and [::1])",
		},
		{
			name: "http remote IP rejected",
			yaml: `
mcp:
  - name: test
    url: http://192.168.1.100:3000/mcp
`,
			wantErr: "mcp[0]: 'url' must use HTTPS (http:// is only allowed for localhost, 127.0.0.1, and [::1])",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "moat.yaml", tt.yaml)

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

func TestIsHostLocalURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"http://localhost:3000/mcp", true},
		{"http://localhost/mcp", true},
		{"http://127.0.0.1:8080/sse", true},
		{"http://127.0.0.1/mcp", true},
		{"http://[::1]:3000/mcp", true},
		{"http://[::1]/mcp", true},            // IPv6 loopback without port
		{"https://localhost:3000/mcp", false}, // HTTPS is not host-local
		{"http://example.com/mcp", false},
		{"http://192.168.1.100:3000/mcp", false},
		{"https://mcp.example.com/mcp", false},
		{"ftp://localhost/file", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := isHostLocalURL(tt.url)
			if got != tt.want {
				t.Errorf("isHostLocalURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestLoadConfigWithHooks(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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
	configPath := filepath.Join(dir, "moat.yaml")

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

func TestLoadConfigWithClaudeBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		wantErr string
	}{
		{
			name:    "valid http localhost",
			baseURL: "http://localhost:8080",
		},
		{
			name:    "valid https proxy",
			baseURL: "https://proxy.internal:3000",
		},
		{
			name:    "valid http with path",
			baseURL: "http://localhost:8080/v1",
		},
		{
			name:    "invalid scheme ftp",
			baseURL: "ftp://localhost:8080",
			wantErr: "scheme must be http or https",
		},
		{
			name:    "missing scheme",
			baseURL: "localhost:8080",
			wantErr: "scheme must be http or https",
		},
		{
			name:    "empty scheme with slashes",
			baseURL: "://localhost:8080",
			wantErr: "invalid URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			content := "agent: claude-code\nclaude:\n  base_url: " + tt.baseURL + "\n"
			os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(content), 0644)

			cfg, err := Load(dir)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Claude.BaseURL != tt.baseURL {
				t.Errorf("BaseURL = %q, want %q", cfg.Claude.BaseURL, tt.baseURL)
			}
		})
	}
}

// --- Additional host-local MCP tests ---

func TestLoad_MCP_MixedHostAndRemote(t *testing.T) {
	// Verify a config with both host-local and remote MCP servers parses correctly.
	dir := t.TempDir()
	writeFile(t, dir, "moat.yaml", `
agent: test
mcp:
  - name: local-tools
    url: http://localhost:3000/mcp
  - name: remote-api
    url: https://mcp.example.com/api
    auth:
      grant: mcp-example
      header: Authorization
  - name: local-auth
    url: http://127.0.0.1:8080/sse
    auth:
      grant: local-key
      header: X-API-Key
`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(cfg.MCP) != 3 {
		t.Fatalf("expected 3 MCP servers, got %d", len(cfg.MCP))
	}

	// Verify host-local without auth
	if cfg.MCP[0].Name != "local-tools" {
		t.Errorf("MCP[0].Name = %q, want %q", cfg.MCP[0].Name, "local-tools")
	}
	if cfg.MCP[0].Auth != nil {
		t.Errorf("MCP[0].Auth should be nil for unauthenticated local server")
	}

	// Verify remote with auth
	if cfg.MCP[1].Auth == nil {
		t.Fatal("MCP[1].Auth should not be nil")
	}
	if cfg.MCP[1].Auth.Grant != "mcp-example" {
		t.Errorf("MCP[1].Auth.Grant = %q, want %q", cfg.MCP[1].Auth.Grant, "mcp-example")
	}

	// Verify host-local with auth
	if cfg.MCP[2].Auth == nil {
		t.Fatal("MCP[2].Auth should not be nil")
	}
	if cfg.MCP[2].Auth.Header != "X-API-Key" {
		t.Errorf("MCP[2].Auth.Header = %q, want %q", cfg.MCP[2].Auth.Header, "X-API-Key")
	}
}

func TestLoad_MCP_HostLocalNoPort(t *testing.T) {
	// Host-local server without explicit port should be accepted.
	dir := t.TempDir()
	writeFile(t, dir, "moat.yaml", `
mcp:
  - name: local-no-port
    url: http://localhost/mcp
`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(cfg.MCP) != 1 {
		t.Fatalf("expected 1 MCP server, got %d", len(cfg.MCP))
	}
	if cfg.MCP[0].URL != "http://localhost/mcp" {
		t.Errorf("URL = %q, want %q", cfg.MCP[0].URL, "http://localhost/mcp")
	}
}

func TestLoad_MCP_HostLocalIPv6(t *testing.T) {
	// IPv6 loopback should be accepted as host-local.
	dir := t.TempDir()
	writeFile(t, dir, "moat.yaml", `
mcp:
  - name: ipv6-local
    url: "http://[::1]:3000/mcp"
`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(cfg.MCP) != 1 {
		t.Fatalf("expected 1 MCP server, got %d", len(cfg.MCP))
	}
}

func TestIsHostLocalURL_EdgeCases(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"http://localhost", true},            // no port, no path
		{"http://localhost/", true},           // no port, root path
		{"http://127.0.0.1", true},            // no port, no path
		{"http://LOCALHOST:3000/mcp", false},  // case-sensitive check
		{"http://localhost.:3000/mcp", false}, // trailing dot
		{"http://0.0.0.0:3000/mcp", false},    // 0.0.0.0 is not localhost
		{"http://10.0.0.1:3000/mcp", false},   // private IP
		{"", false},                           // empty string
		{"not-a-url", false},                  // not a URL
		{"http://", false},                    // empty host
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := isHostLocalURL(tt.url)
			if got != tt.want {
				t.Errorf("isHostLocalURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestLoadConfig_LanguageServers(t *testing.T) {
	t.Run("valid language server", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(`
agent: claude
dependencies:
  - go
language_servers:
  - go
`), 0644)

		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if len(cfg.LanguageServers) != 1 {
			t.Fatalf("LanguageServers length = %d, want 1", len(cfg.LanguageServers))
		}
		if cfg.LanguageServers[0] != "go" {
			t.Errorf("LanguageServers[0] = %q, want %q", cfg.LanguageServers[0], "go")
		}
	})

	t.Run("unknown language server", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(`
agent: claude
language_servers:
  - unknown-lsp
`), 0644)

		_, err := Load(dir)
		if err == nil {
			t.Error("Load() should return error for unknown language server")
		}
		if !strings.Contains(err.Error(), "unknown language server") {
			t.Errorf("error should mention unknown language server, got: %v", err)
		}
	})

	t.Run("empty language servers", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(`
agent: claude
`), 0644)

		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if len(cfg.LanguageServers) != 0 {
			t.Errorf("LanguageServers = %v, want empty", cfg.LanguageServers)
		}
	})

	t.Run("duplicate language servers rejected", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(`
agent: claude
language_servers:
  - go
  - go
`), 0644)

		_, err := Load(dir)
		if err == nil {
			t.Fatal("Load() should return error for duplicate language servers")
		}
		if !strings.Contains(err.Error(), "duplicate language server") {
			t.Errorf("error should mention duplicate language server, got: %v", err)
		}
	})

	t.Run("language servers with other config", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(`
agent: claude
dependencies:
  - node@20
  - go
grants:
  - anthropic
language_servers:
  - go
`), 0644)

		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if len(cfg.LanguageServers) != 1 || cfg.LanguageServers[0] != "go" {
			t.Errorf("LanguageServers = %v, want [go]", cfg.LanguageServers)
		}
		// Other fields should be unaffected
		if len(cfg.Dependencies) != 2 {
			t.Errorf("Dependencies = %v, want 2 entries", cfg.Dependencies)
		}
		if len(cfg.Grants) != 1 {
			t.Errorf("Grants = %v, want 1 entry", cfg.Grants)
		}
	})
}

func TestLoadLegacyAgentYaml(t *testing.T) {
	// When only agent.yaml exists (no moat.yaml), Load should still work
	// using the legacy filename as a fallback.
	dir := t.TempDir()
	configPath := filepath.Join(dir, LegacyConfigFilename)

	content := `
agent: legacy-agent
version: 1.0.0
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load should succeed with legacy agent.yaml: %v", err)
	}
	if cfg.Agent != "legacy-agent" {
		t.Errorf("Agent = %q, want %q", cfg.Agent, "legacy-agent")
	}
}

func TestLoadMoatYamlPreferred(t *testing.T) {
	// When both moat.yaml and agent.yaml exist, moat.yaml should be used.
	dir := t.TempDir()

	moatContent := `
agent: moat-agent
version: 2.0.0
`
	legacyContent := `
agent: legacy-agent
version: 1.0.0
`
	os.WriteFile(filepath.Join(dir, ConfigFilename), []byte(moatContent), 0644)
	os.WriteFile(filepath.Join(dir, LegacyConfigFilename), []byte(legacyContent), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agent != "moat-agent" {
		t.Errorf("Agent = %q, want %q (moat.yaml should be preferred over agent.yaml)", cfg.Agent, "moat-agent")
	}
	if cfg.Version != "2.0.0" {
		t.Errorf("Version = %q, want %q", cfg.Version, "2.0.0")
	}
}

func TestLoad_MCP_HostLocalDuplicateNames(t *testing.T) {
	// Duplicate names should be rejected even for host-local servers.
	dir := t.TempDir()
	writeFile(t, dir, "moat.yaml", `
mcp:
  - name: local
    url: http://localhost:3000/mcp
  - name: local
    url: http://localhost:4000/mcp
`)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for duplicate MCP server names")
	}
	if !strings.Contains(err.Error(), "duplicate name 'local'") {
		t.Errorf("expected error about duplicate name, got: %v", err)
	}
}

func TestLoadConfigWithUlimits(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "moat.yaml")

	content := `
agent: claude-code
container:
  ulimits:
    nofile:
      soft: 1024
      hard: 65536
    nproc:
      soft: 4096
      hard: 4096
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Container.Ulimits) != 2 {
		t.Fatalf("Ulimits = %d, want 2", len(cfg.Container.Ulimits))
	}
	nofile, ok := cfg.Container.Ulimits["nofile"]
	if !ok {
		t.Fatal("missing nofile ulimit")
	}
	if nofile.Soft != 1024 || nofile.Hard != 65536 {
		t.Errorf("nofile = %+v, want soft=1024 hard=65536", nofile)
	}
	nproc, ok := cfg.Container.Ulimits["nproc"]
	if !ok {
		t.Fatal("missing nproc ulimit")
	}
	if nproc.Soft != 4096 || nproc.Hard != 4096 {
		t.Errorf("nproc = %+v, want soft=4096 hard=4096", nproc)
	}
}

func TestLoadConfigUlimitValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "soft exceeds hard",
			yaml: `
agent: test
container:
  ulimits:
    nofile:
      soft: 65536
      hard: 1024
`,
			wantErr: "container.ulimits.nofile: soft limit (65536) must not exceed hard limit (1024)",
		},
		{
			name: "negative soft",
			yaml: `
agent: test
container:
  ulimits:
    nofile:
      soft: -2
      hard: 1024
`,
			wantErr: "container.ulimits.nofile: soft limit must be -1 (unlimited) or non-negative",
		},
		{
			name: "unknown ulimit name",
			yaml: `
agent: test
container:
  ulimits:
    bogus:
      soft: 100
      hard: 100
`,
			wantErr: `container.ulimits: unknown ulimit "bogus"`,
		},
		{
			name: "negative hard",
			yaml: `
agent: test
container:
  ulimits:
    nofile:
      soft: 1024
      hard: -2
`,
			wantErr: "container.ulimits.nofile: hard limit must be -1 (unlimited) or non-negative",
		},
		{
			name: "unlimited soft with finite hard",
			yaml: `
agent: test
container:
  ulimits:
    nofile:
      soft: -1
      hard: 1024
`,
			wantErr: "container.ulimits.nofile: soft limit (unlimited) must not exceed hard limit (1024)",
		},
		{
			name: "unlimited is valid",
			yaml: `
agent: test
container:
  ulimits:
    memlock:
      soft: -1
      hard: -1
`,
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(tt.yaml), 0644)
			_, err := Load(dir)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoad_MCP_HttpsLocalhostNotHostLocal(t *testing.T) {
	// https://localhost should be treated as a remote server (not host-local),
	// and should be accepted since it uses HTTPS.
	dir := t.TempDir()
	writeFile(t, dir, "moat.yaml", `
mcp:
  - name: secure-local
    url: https://localhost:3000/mcp
`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(cfg.MCP) != 1 {
		t.Fatalf("expected 1 MCP server, got %d", len(cfg.MCP))
	}
}

func TestClipboardConfig(t *testing.T) {
	t.Run("clipboard false parses as bool pointer", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, "moat.yaml")
		os.WriteFile(configPath, []byte(`
agent: claude-code
clipboard: false
`), 0644)

		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Clipboard == nil {
			t.Fatal("Clipboard should not be nil when explicitly set to false")
		}
		if *cfg.Clipboard != false {
			t.Errorf("Clipboard = %v, want false", *cfg.Clipboard)
		}
	})

	t.Run("clipboard omitted defaults to nil", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, "moat.yaml")
		os.WriteFile(configPath, []byte(`
agent: claude-code
`), 0644)

		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Clipboard != nil {
			t.Errorf("Clipboard should be nil when omitted, got %v", *cfg.Clipboard)
		}
	})
}

func TestServiceSpecUnmarshalWithExtra(t *testing.T) {
	input := `
env:
  FOO: bar
wait: false
models:
  - qwen2.5-coder:7b
  - nomic-embed-text
`
	var spec ServiceSpec
	err := yaml.Unmarshal([]byte(input), &spec)
	require.NoError(t, err)
	assert.Equal(t, "bar", spec.Env["FOO"])
	assert.False(t, spec.ServiceWait())
	assert.Equal(t, []string{"qwen2.5-coder:7b", "nomic-embed-text"}, spec.Extra["models"])
}

func TestServiceSpecUnmarshalNoExtra(t *testing.T) {
	input := `
env:
  POSTGRES_DB: myapp
`
	var spec ServiceSpec
	err := yaml.Unmarshal([]byte(input), &spec)
	require.NoError(t, err)
	assert.Equal(t, "myapp", spec.Env["POSTGRES_DB"])
	assert.Empty(t, spec.Extra)
}

func TestServiceSpecUnmarshalPreservesImage(t *testing.T) {
	input := `
image: custom-postgres:17
env:
  POSTGRES_DB: myapp
`
	var spec ServiceSpec
	err := yaml.Unmarshal([]byte(input), &spec)
	require.NoError(t, err)
	assert.Equal(t, "custom-postgres:17", spec.Image)
	assert.Equal(t, "myapp", spec.Env["POSTGRES_DB"])
}

func TestServiceSpecUnmarshalEmptyExtra(t *testing.T) {
	input := `
env:
  FOO: bar
models: []
`
	var spec ServiceSpec
	err := yaml.Unmarshal([]byte(input), &spec)
	require.NoError(t, err)
	assert.Empty(t, spec.Extra["models"])
}

func TestServiceSpecUnmarshalCapturesScalarKeys(t *testing.T) {
	input := `
env:
  FOO: bar
typo_key: some_value
`
	var spec ServiceSpec
	err := yaml.Unmarshal([]byte(input), &spec)
	require.NoError(t, err)
	// Unknown scalar keys are captured in Extra with nil value
	assert.Contains(t, spec.Extra, "typo_key")
	assert.Nil(t, spec.Extra["typo_key"])
}

func TestServiceSpecUnmarshalMemoryNotLeakedToExtra(t *testing.T) {
	// memory: is a known ServiceSpec field — it must not leak into Extra.
	// If it did, buildServiceConfig would reject it as an unknown key when
	// combined with a provisions-capable service like ollama.
	input := `
memory: 2048
models:
  - qwen2.5-coder:1.5b
`
	var spec ServiceSpec
	err := yaml.Unmarshal([]byte(input), &spec)
	require.NoError(t, err)
	assert.Equal(t, 2048, spec.Memory)
	assert.NotContains(t, spec.Extra, "memory", "memory must not appear in Extra")
	assert.Equal(t, []string{"qwen2.5-coder:1.5b"}, spec.Extra["models"])
}
