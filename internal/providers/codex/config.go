package codex

import (
	"fmt"
	"os"
	"path/filepath"
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
