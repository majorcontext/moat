package gemini

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
