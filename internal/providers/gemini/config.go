package gemini

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
)

// MCPConfig represents the MCP configuration structure for Gemini.
type MCPConfig struct {
	MCPServers map[string]MCPServer `json:"mcpServers"`
}

// MCPServer represents a single MCP server configuration.
type MCPServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
}

// GenerateMCPConfig generates MCP server configuration for Gemini.
// Returns nil if no MCP servers are configured.
func GenerateMCPConfig(cfg *config.Config, grants []string) ([]byte, error) {
	if cfg == nil || len(cfg.Gemini.MCP) == 0 {
		return nil, nil
	}

	grantSet := make(map[string]bool)
	for _, g := range grants {
		grantSet[g] = true
	}

	mcpConfig := MCPConfig{
		MCPServers: make(map[string]MCPServer),
	}

	for name, spec := range cfg.Gemini.MCP {
		if spec.Grant != "" && !grantSet[spec.Grant] {
			return nil, fmt.Errorf("MCP server %q requires grant %q but it was not provided", name, spec.Grant)
		}

		server := MCPServer{
			Command: spec.Command,
			Args:    spec.Args,
			Cwd:     spec.Cwd,
		}

		if len(spec.Env) > 0 {
			server.Env = make(map[string]string)
			for k, v := range spec.Env {
				server.Env[k] = v
			}
		}

		if spec.Grant != "" {
			if server.Env == nil {
				server.Env = make(map[string]string)
			}
			switch spec.Grant {
			case "github":
				server.Env["GITHUB_TOKEN"] = credential.ProxyInjectedPlaceholder
			case "gemini":
				server.Env["GEMINI_API_KEY"] = credential.ProxyInjectedPlaceholder
			case "anthropic":
				server.Env["ANTHROPIC_API_KEY"] = credential.ProxyInjectedPlaceholder
			}
		}

		mcpConfig.MCPServers[name] = server
	}

	return json.MarshalIndent(mcpConfig, "", "  ")
}

// WriteMCPConfig writes the MCP configuration to the workspace.
func WriteMCPConfig(workspaceDir string, mcpJSON []byte) error {
	if len(mcpJSON) == 0 {
		return nil
	}

	mcpPath := filepath.Join(workspaceDir, ".mcp.json")
	if err := os.WriteFile(mcpPath, mcpJSON, 0644); err != nil {
		return fmt.Errorf("writing .mcp.json: %w", err)
	}

	return nil
}
