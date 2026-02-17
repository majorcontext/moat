package gemini

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/provider"
)

func TestPrepareContainer_LocalMCP(t *testing.T) {
	p := &Provider{}

	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{
		ContainerHome: "/home/moatuser",
		LocalMCPServers: map[string]provider.LocalMCPServerConfig{
			"my-tools": {
				Command: "/usr/local/bin/gemini-mcp",
				Args:    []string{"--port", "3000"},
				Env:     map[string]string{"DEBUG": "true"},
				Cwd:     "/workspace",
			},
		},
	})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()

	// Verify mcp.json was written to staging dir
	mcpPath := filepath.Join(cfg.StagingDir, "mcp.json")
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("mcp.json not found in staging dir: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, `"my-tools"`) {
		t.Error("mcp.json should contain my-tools server")
	}
	if !strings.Contains(content, `"command": "/usr/local/bin/gemini-mcp"`) {
		t.Errorf("mcp.json should contain command, got: %s", content)
	}
	if !strings.Contains(content, `"--port"`) {
		t.Error("mcp.json should contain args")
	}
	if !strings.Contains(content, `"DEBUG": "true"`) {
		t.Error("mcp.json should contain env")
	}
}

func TestPrepareContainer_NoLocalMCP(t *testing.T) {
	p := &Provider{}

	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{
		ContainerHome: "/home/moatuser",
	})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()

	// mcp.json should NOT exist when no local MCP servers
	mcpPath := filepath.Join(cfg.StagingDir, "mcp.json")
	if _, err := os.Stat(mcpPath); err == nil {
		t.Error("mcp.json should NOT exist when no local MCP servers configured")
	}
}

func TestPrepareContainer_LocalMCP_MultipleServers(t *testing.T) {
	p := &Provider{}

	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{
		ContainerHome: "/home/moatuser",
		LocalMCPServers: map[string]provider.LocalMCPServerConfig{
			"alpha": {
				Command: "alpha-mcp",
			},
			"beta": {
				Command: "beta-mcp",
				Args:    []string{"--verbose"},
			},
		},
	})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	defer cfg.Cleanup()

	data, err := os.ReadFile(filepath.Join(cfg.StagingDir, "mcp.json"))
	if err != nil {
		t.Fatalf("mcp.json not found: %v", err)
	}

	var mcpCfg MCPConfig
	if err := json.Unmarshal(data, &mcpCfg); err != nil {
		t.Fatalf("failed to parse mcp.json: %v", err)
	}

	if len(mcpCfg.MCPServers) != 2 {
		t.Errorf("expected 2 MCP servers, got %d", len(mcpCfg.MCPServers))
	}
	if _, ok := mcpCfg.MCPServers["alpha"]; !ok {
		t.Error("alpha server should be present")
	}
	if _, ok := mcpCfg.MCPServers["beta"]; !ok {
		t.Error("beta server should be present")
	}
	if mcpCfg.MCPServers["beta"].Args[0] != "--verbose" {
		t.Errorf("beta args[0] = %q, want '--verbose'", mcpCfg.MCPServers["beta"].Args[0])
	}
}

func TestGenerateMCPConfig_NilConfig(t *testing.T) {
	got, err := GenerateMCPConfig(nil, nil)
	if err != nil {
		t.Fatalf("GenerateMCPConfig() error = %v", err)
	}
	if got != nil {
		t.Errorf("GenerateMCPConfig(nil) = %v, want nil", got)
	}
}

func TestGenerateMCPConfig_EmptyMCP(t *testing.T) {
	cfg := &config.Config{
		Gemini: config.GeminiConfig{
			MCP: map[string]config.MCPServerSpec{},
		},
	}
	got, err := GenerateMCPConfig(cfg, nil)
	if err != nil {
		t.Fatalf("GenerateMCPConfig() error = %v", err)
	}
	if got != nil {
		t.Errorf("GenerateMCPConfig(empty) = %v, want nil", got)
	}
}

func TestGenerateMCPConfig_WithGrant(t *testing.T) {
	cfg := &config.Config{
		Gemini: config.GeminiConfig{
			MCP: map[string]config.MCPServerSpec{
				"gh-tools": {
					Command: "gh-mcp",
					Grant:   "github",
				},
			},
		},
	}

	got, err := GenerateMCPConfig(cfg, []string{"github"})
	if err != nil {
		t.Fatalf("GenerateMCPConfig() error = %v", err)
	}
	if got == nil {
		t.Fatal("GenerateMCPConfig() = nil, want non-nil")
	}

	var mcpCfg MCPConfig
	if err := json.Unmarshal(got, &mcpCfg); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	server, ok := mcpCfg.MCPServers["gh-tools"]
	if !ok {
		t.Fatal("gh-tools server should be present")
	}
	if server.Env["GITHUB_TOKEN"] == "" {
		t.Error("GITHUB_TOKEN should be set in env for github grant")
	}
}

func TestGenerateMCPConfig_MissingGrant(t *testing.T) {
	cfg := &config.Config{
		Gemini: config.GeminiConfig{
			MCP: map[string]config.MCPServerSpec{
				"gh-tools": {
					Command: "gh-mcp",
					Grant:   "github",
				},
			},
		},
	}

	_, err := GenerateMCPConfig(cfg, []string{}) // github not in grants
	if err == nil {
		t.Error("GenerateMCPConfig() should return error for missing grant")
	}
	if !strings.Contains(err.Error(), "github") {
		t.Errorf("error should mention missing grant 'github', got: %v", err)
	}
}

func TestGenerateMCPConfig_WithEnvAndCwd(t *testing.T) {
	cfg := &config.Config{
		Gemini: config.GeminiConfig{
			MCP: map[string]config.MCPServerSpec{
				"my-server": {
					Command: "my-mcp",
					Args:    []string{"--flag"},
					Env:     map[string]string{"CUSTOM_VAR": "value"},
					Cwd:     "/opt/tools",
				},
			},
		},
	}

	got, err := GenerateMCPConfig(cfg, nil)
	if err != nil {
		t.Fatalf("GenerateMCPConfig() error = %v", err)
	}

	var mcpCfg MCPConfig
	if err := json.Unmarshal(got, &mcpCfg); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	server := mcpCfg.MCPServers["my-server"]
	if server.Command != "my-mcp" {
		t.Errorf("command = %q, want 'my-mcp'", server.Command)
	}
	if len(server.Args) != 1 || server.Args[0] != "--flag" {
		t.Errorf("args = %v, want ['--flag']", server.Args)
	}
	if server.Env["CUSTOM_VAR"] != "value" {
		t.Errorf("env CUSTOM_VAR = %q, want 'value'", server.Env["CUSTOM_VAR"])
	}
	if server.Cwd != "/opt/tools" {
		t.Errorf("cwd = %q, want '/opt/tools'", server.Cwd)
	}
}

func TestWriteMCPConfig(t *testing.T) {
	t.Run("nil data is no-op", func(t *testing.T) {
		dir := t.TempDir()
		err := WriteMCPConfig(dir, nil)
		if err != nil {
			t.Fatalf("WriteMCPConfig(nil) error = %v", err)
		}

		if _, err := os.Stat(filepath.Join(dir, ".mcp.json")); err == nil {
			t.Error(".mcp.json should not exist for nil data")
		}
	})

	t.Run("writes to workspace", func(t *testing.T) {
		dir := t.TempDir()
		data := []byte(`{"mcpServers":{"test":{"command":"test-cmd"}}}`)

		err := WriteMCPConfig(dir, data)
		if err != nil {
			t.Fatalf("WriteMCPConfig() error = %v", err)
		}

		got, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
		if err != nil {
			t.Fatalf("failed to read .mcp.json: %v", err)
		}
		if string(got) != string(data) {
			t.Errorf("got %q, want %q", got, data)
		}
	})
}
