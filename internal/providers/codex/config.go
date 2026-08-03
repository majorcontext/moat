package codex

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// WorkspacePath is where moat mounts the workspace inside the container.
//
// The generated config pre-trusts this path. That serves two purposes, and the
// second is easy to miss: besides skipping the interactive trust prompt (Codex
// only loads project-scoped `.codex/` layers for trusted projects), it is what
// lets `codex exec` run in a workspace that is not a git repository. Without a
// trust entry, `codex exec` exits 1 with "Not inside a trusted directory and
// --skip-git-repo-check was not specified" before doing any work.
const WorkspacePath = "/workspace"

// Approval policies and sandbox modes accepted by the Codex CLI.
// See https://developers.openai.com/codex/config-reference.
const (
	ApprovalNever     = "never"
	ApprovalOnRequest = "on-request"

	SandboxFullAccess     = "danger-full-access"
	SandboxWorkspaceWrite = "workspace-write"
)

// configHeader is prepended to the generated config.toml. go-toml does not
// emit comments, so it is written ahead of the marshaled body.
const configHeader = `# Moat-generated Codex configuration - do not edit.
# Real authentication is handled by the Moat proxy.

`

// Config is the subset of ~/.codex/config.toml that moat generates.
//
// Scalar keys must stay ahead of the table-valued ones: TOML assigns bare
// key/value pairs to the table they follow, so a scalar emitted after
// [shell_environment_policy] would land inside it.
type Config struct {
	ApprovalPolicy         string                   `toml:"approval_policy"`
	SandboxMode            string                   `toml:"sandbox_mode"`
	ShellEnvironmentPolicy ShellEnvironmentPolicy   `toml:"shell_environment_policy"`
	Projects               map[string]ProjectConfig `toml:"projects,omitempty"`
	MCPServers             map[string]MCPServer     `toml:"mcp_servers,omitempty"`
}

// ShellEnvironmentPolicy controls the environment Codex passes to the commands
// it runs.
type ShellEnvironmentPolicy struct {
	Inherit string `toml:"inherit"`
}

// ProjectConfig marks a project path trusted or untrusted.
type ProjectConfig struct {
	TrustLevel string `toml:"trust_level"`
}

// MCPServer is a single entry under [mcp_servers]. Command/Args/Env/Cwd
// describe a stdio server; URL/HTTPHeaders describe a streamable HTTP server.
// Codex reads MCP servers from config.toml only - it ignores the .mcp.json
// file other agents use.
type MCPServer struct {
	Command     string            `toml:"command,omitempty"`
	Args        []string          `toml:"args,omitempty"`
	Env         map[string]string `toml:"env,omitempty"`
	Cwd         string            `toml:"cwd,omitempty"`
	URL         string            `toml:"url,omitempty"`
	HTTPHeaders map[string]string `toml:"http_headers,omitempty"`
}

// NewConfig returns the base Codex configuration for a moat container.
//
// requireApproval selects between the two policies moat supports:
//
//   - false (default): approvals off and Codex's own sandbox off. The
//     container is already the isolation boundary, and Codex's sandbox blocks
//     the network the moat proxy provides, so nesting it breaks any command
//     the agent runs that needs the network or writes outside the workspace.
//   - true (--noyolo): Codex's own defaults, so it prompts before acting and
//     confines commands to the workspace with the network off.
//
// The environment policy is always "all". Codex's "core" default keeps only
// PATH/HOME/SHELL/TMPDIR/LANG/LOGNAME/USER, which strips HTTP_PROXY,
// SSL_CERT_FILE, and NODE_EXTRA_CA_CERTS from every command Codex spawns -
// i.e. exactly the variables that route a container command through the moat
// proxy. moat curates the container environment already.
func NewConfig(requireApproval bool) Config {
	cfg := Config{
		ApprovalPolicy:         ApprovalNever,
		SandboxMode:            SandboxFullAccess,
		ShellEnvironmentPolicy: ShellEnvironmentPolicy{Inherit: "all"},
		Projects: map[string]ProjectConfig{
			WorkspacePath: {TrustLevel: "trusted"},
		},
	}
	if requireApproval {
		cfg.ApprovalPolicy = ApprovalOnRequest
		cfg.SandboxMode = SandboxWorkspaceWrite
	}
	return cfg
}

// WriteCodexConfig writes cfg as config.toml in the staging directory.
// moat-init copies it to ~/.codex/config.toml at container startup.
func WriteCodexConfig(stagingDir string, cfg Config) error {
	body, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling codex config: %w", err)
	}

	content := append([]byte(configHeader), body...)
	if err := os.WriteFile(filepath.Join(stagingDir, "config.toml"), content, 0o600); err != nil {
		return fmt.Errorf("writing config.toml: %w", err)
	}

	return nil
}
