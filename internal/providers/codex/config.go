package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/config"
)

// WriteCodexConfig writes a minimal ~/.codex/config.toml to the staging directory.
// This provides default settings for the Codex CLI.
func WriteCodexConfig(stagingDir string) error {
	// Minimal config to set up Codex with sensible defaults
	// Using TOML format as Codex expects
	configContent := `# Moat-generated Codex configuration
# Real authentication is handled by the Moat proxy

[shell_environment_policy]
inherit = "core"
`

	if err := os.WriteFile(filepath.Join(stagingDir, "config.toml"), []byte(configContent), 0600); err != nil {
		return fmt.Errorf("writing config.toml: %w", err)
	}

	return nil
}

// MCPConfig represents the MCP configuration structure for Codex.
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

// GenerateMCPConfig generates MCP server configuration for Codex.
// Returns nil if no MCP servers are configured.
func GenerateMCPConfig(cfg *config.Config, grants []string) ([]byte, error) {
	if cfg == nil || len(cfg.Codex.MCP) == 0 {
		return nil, nil
	}

	// Build set of available grants for quick lookup
	grantSet := make(map[string]bool)
	for _, g := range grants {
		grantSet[g] = true
	}

	mcpConfig := MCPConfig{
		MCPServers: make(map[string]MCPServer),
	}

	for name, spec := range cfg.Codex.MCP {
		// Validate that required grants are present
		if spec.Grant != "" && !grantSet[spec.Grant] {
			return nil, fmt.Errorf("MCP server %q requires grant %q but it was not provided", name, spec.Grant)
		}

		server := MCPServer{
			Command: spec.Command,
			Args:    spec.Args,
			Cwd:     spec.Cwd,
		}

		// Copy environment variables
		if len(spec.Env) > 0 {
			server.Env = make(map[string]string)
			for k, v := range spec.Env {
				server.Env[k] = v
			}
		}

		// Inject credential placeholders based on grant
		if spec.Grant != "" {
			if server.Env == nil {
				server.Env = make(map[string]string)
			}
			switch spec.Grant {
			case "github":
				server.Env["GITHUB_TOKEN"] = ProxyInjectedPlaceholder
			case "openai":
				server.Env["OPENAI_API_KEY"] = ProxyInjectedPlaceholder
			case "anthropic":
				server.Env["ANTHROPIC_API_KEY"] = ProxyInjectedPlaceholder
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
