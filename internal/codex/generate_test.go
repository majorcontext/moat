package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/andybons/moat/internal/config"
	"github.com/andybons/moat/internal/credential"
)

func TestGenerateMCPConfig_NilConfig(t *testing.T) {
	result, err := GenerateMCPConfig(nil, nil)
	if err != nil {
		t.Fatalf("GenerateMCPConfig(nil, nil) error = %v", err)
	}
	if result != nil {
		t.Errorf("GenerateMCPConfig(nil, nil) = %v, want nil", result)
	}
}

func TestGenerateMCPConfig_EmptyMCP(t *testing.T) {
	cfg := &config.Config{}
	result, err := GenerateMCPConfig(cfg, nil)
	if err != nil {
		t.Fatalf("GenerateMCPConfig() error = %v", err)
	}
	if result != nil {
		t.Errorf("GenerateMCPConfig() = %v, want nil", result)
	}
}

func TestGenerateMCPConfig_BasicServer(t *testing.T) {
	cfg := &config.Config{
		Codex: config.CodexConfig{
			MCP: map[string]config.MCPServerSpec{
				"test-server": {
					Command: "node",
					Args:    []string{"server.js"},
					Cwd:     "/app",
				},
			},
		},
	}

	result, err := GenerateMCPConfig(cfg, nil)
	if err != nil {
		t.Fatalf("GenerateMCPConfig() error = %v", err)
	}
	if result == nil {
		t.Fatal("GenerateMCPConfig() returned nil, want config")
	}

	var mcpConfig MCPConfig
	if err := json.Unmarshal(result, &mcpConfig); err != nil {
		t.Fatalf("Failed to parse MCP config: %v", err)
	}

	server, ok := mcpConfig.MCPServers["test-server"]
	if !ok {
		t.Fatal("MCP config missing 'test-server'")
	}
	if server.Command != "node" {
		t.Errorf("Command = %q, want %q", server.Command, "node")
	}
	if len(server.Args) != 1 || server.Args[0] != "server.js" {
		t.Errorf("Args = %v, want [server.js]", server.Args)
	}
	if server.Cwd != "/app" {
		t.Errorf("Cwd = %q, want %q", server.Cwd, "/app")
	}
}

func TestGenerateMCPConfig_WithEnv(t *testing.T) {
	cfg := &config.Config{
		Codex: config.CodexConfig{
			MCP: map[string]config.MCPServerSpec{
				"env-server": {
					Command: "server",
					Env: map[string]string{
						"FOO": "bar",
						"BAZ": "qux",
					},
				},
			},
		},
	}

	result, err := GenerateMCPConfig(cfg, nil)
	if err != nil {
		t.Fatalf("GenerateMCPConfig() error = %v", err)
	}

	var mcpConfig MCPConfig
	if err := json.Unmarshal(result, &mcpConfig); err != nil {
		t.Fatalf("Failed to parse MCP config: %v", err)
	}

	server := mcpConfig.MCPServers["env-server"]
	if server.Env["FOO"] != "bar" {
		t.Errorf("Env[FOO] = %q, want %q", server.Env["FOO"], "bar")
	}
	if server.Env["BAZ"] != "qux" {
		t.Errorf("Env[BAZ] = %q, want %q", server.Env["BAZ"], "qux")
	}
}

func TestGenerateMCPConfig_WithGrant(t *testing.T) {
	tests := []struct {
		name      string
		grant     string
		grantList []string
		wantEnv   string
		wantValue string
	}{
		{
			name:      "github grant",
			grant:     "github",
			grantList: []string{"github"},
			wantEnv:   "GITHUB_TOKEN",
			wantValue: credential.ProxyInjectedPlaceholder,
		},
		{
			name:      "openai grant",
			grant:     "openai",
			grantList: []string{"openai"},
			wantEnv:   "OPENAI_API_KEY",
			wantValue: credential.ProxyInjectedPlaceholder,
		},
		{
			name:      "anthropic grant",
			grant:     "anthropic",
			grantList: []string{"anthropic"},
			wantEnv:   "ANTHROPIC_API_KEY",
			wantValue: credential.ProxyInjectedPlaceholder,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Codex: config.CodexConfig{
					MCP: map[string]config.MCPServerSpec{
						"test": {
							Command: "server",
							Grant:   tt.grant,
						},
					},
				},
			}

			result, err := GenerateMCPConfig(cfg, tt.grantList)
			if err != nil {
				t.Fatalf("GenerateMCPConfig() error = %v", err)
			}

			var mcpConfig MCPConfig
			if err := json.Unmarshal(result, &mcpConfig); err != nil {
				t.Fatalf("Failed to parse MCP config: %v", err)
			}

			server := mcpConfig.MCPServers["test"]
			if server.Env[tt.wantEnv] != tt.wantValue {
				t.Errorf("Env[%s] = %q, want %q", tt.wantEnv, server.Env[tt.wantEnv], tt.wantValue)
			}
		})
	}
}

func TestGenerateMCPConfig_MissingGrant(t *testing.T) {
	cfg := &config.Config{
		Codex: config.CodexConfig{
			MCP: map[string]config.MCPServerSpec{
				"requires-github": {
					Command: "server",
					Grant:   "github",
				},
			},
		},
	}

	// No grants provided
	_, err := GenerateMCPConfig(cfg, nil)
	if err == nil {
		t.Error("GenerateMCPConfig() expected error for missing grant, got nil")
	}

	// Wrong grant provided
	_, err = GenerateMCPConfig(cfg, []string{"openai"})
	if err == nil {
		t.Error("GenerateMCPConfig() expected error for wrong grant, got nil")
	}
}

func TestGenerateMCPConfig_MultipleServers(t *testing.T) {
	cfg := &config.Config{
		Codex: config.CodexConfig{
			MCP: map[string]config.MCPServerSpec{
				"server1": {Command: "cmd1"},
				"server2": {Command: "cmd2"},
				"server3": {Command: "cmd3"},
			},
		},
	}

	result, err := GenerateMCPConfig(cfg, nil)
	if err != nil {
		t.Fatalf("GenerateMCPConfig() error = %v", err)
	}

	var mcpConfig MCPConfig
	if err := json.Unmarshal(result, &mcpConfig); err != nil {
		t.Fatalf("Failed to parse MCP config: %v", err)
	}

	if len(mcpConfig.MCPServers) != 3 {
		t.Errorf("MCPServers count = %d, want 3", len(mcpConfig.MCPServers))
	}

	for name, expected := range map[string]string{"server1": "cmd1", "server2": "cmd2", "server3": "cmd3"} {
		if server, ok := mcpConfig.MCPServers[name]; !ok {
			t.Errorf("Missing server %q", name)
		} else if server.Command != expected {
			t.Errorf("Server %q command = %q, want %q", name, server.Command, expected)
		}
	}
}

func TestWriteMCPConfig_Empty(t *testing.T) {
	dir := t.TempDir()
	err := WriteMCPConfig(dir, nil)
	if err != nil {
		t.Fatalf("WriteMCPConfig(nil) error = %v", err)
	}

	// Should not create file
	mcpPath := filepath.Join(dir, ".mcp.json")
	if _, err := os.Stat(mcpPath); !os.IsNotExist(err) {
		t.Error("WriteMCPConfig(nil) should not create file")
	}
}

func TestWriteMCPConfig_Success(t *testing.T) {
	dir := t.TempDir()
	content := []byte(`{"mcpServers": {}}`)

	err := WriteMCPConfig(dir, content)
	if err != nil {
		t.Fatalf("WriteMCPConfig() error = %v", err)
	}

	mcpPath := filepath.Join(dir, ".mcp.json")
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("Failed to read .mcp.json: %v", err)
	}

	if string(data) != string(content) {
		t.Errorf("File content = %q, want %q", string(data), string(content))
	}
}

func TestWriteMCPConfig_InvalidDir(t *testing.T) {
	err := WriteMCPConfig("/nonexistent/path/that/does/not/exist", []byte("test"))
	if err == nil {
		t.Error("WriteMCPConfig() expected error for invalid directory, got nil")
	}
}

func TestGeneratedConfig_Cleanup(t *testing.T) {
	// Create a temp directory
	tempDir := t.TempDir()
	subDir := filepath.Join(tempDir, "cleanup-test")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	// Create a test file
	testFile := filepath.Join(subDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	config := &GeneratedConfig{
		StagingDir: subDir,
		TempDir:    subDir,
	}

	config.Cleanup()

	// Directory should be removed
	if _, err := os.Stat(subDir); !os.IsNotExist(err) {
		t.Error("Cleanup() should remove TempDir")
	}
}

func TestGeneratedConfig_Cleanup_EmptyTempDir(t *testing.T) {
	config := &GeneratedConfig{
		StagingDir: "",
		TempDir:    "",
	}

	// Should not panic with empty TempDir
	config.Cleanup()
}
