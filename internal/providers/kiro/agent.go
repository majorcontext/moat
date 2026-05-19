package kiro

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/provider"
)

// mcpLocalServer is a stdio MCP server entry in ~/.kiro/settings/mcp.json.
type mcpLocalServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
}

// mcpHTTPServer is a remote HTTP MCP server entry. kiro-cli supports remote
// HTTP MCP servers natively; key names follow
// https://kiro.dev/docs/cli/mcp/configuration/#remote-server. That site is
// not reachable from the moat sandbox, so the exact keys remain a documented
// verification point (design spec, Verification §2).
type mcpHTTPServer struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

type mcpFile struct {
	MCPServers map[string]any `json:"mcpServers"`
}

// PrepareContainer stages a ~/.kiro tree (settings, agents, steering) that
// moat-init copies into the container. The real token is never written —
// auth is via the proxy.
func (p *Provider) PrepareContainer(ctx context.Context, opts provider.PrepareOpts) (*provider.ContainerConfig, error) {
	tmpDir, err := os.MkdirTemp("", "moat-kiro-staging-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	for _, sub := range []string{"settings", "agents", "steering"} {
		if mkErr := os.MkdirAll(filepath.Join(tmpDir, sub), 0o755); mkErr != nil {
			cleanup()
			return nil, fmt.Errorf("creating %s dir: %w", sub, mkErr)
		}
	}

	// settings/cli.json — allow --trust-all-tools non-interactively.
	cliJSON, cErr := json.MarshalIndent(map[string]any{
		"chat.disableTrustAllConfirmation": true,
	}, "", "  ")
	if cErr != nil {
		cleanup()
		return nil, fmt.Errorf("marshaling cli.json: %w", cErr)
	}
	if wErr := os.WriteFile(filepath.Join(tmpDir, "settings", "cli.json"), cliJSON, 0o600); wErr != nil {
		cleanup()
		return nil, fmt.Errorf("writing cli.json: %w", wErr)
	}

	// settings/mcp.json — always written. agents/default.json sets
	// includeMcpJson:true, so kiro-cli expects this file even when no servers
	// are configured (unlike codex, whose base config lives in config.toml).
	// An empty {"mcpServers":{}} is valid.
	servers := map[string]any{}
	for name, c := range opts.LocalMCPServers {
		servers[name] = mcpLocalServer{Command: c.Command, Args: c.Args, Env: c.Env, Cwd: c.Cwd}
	}
	for name, c := range opts.MCPServers {
		servers[name] = mcpHTTPServer{URL: c.URL, Headers: c.Headers}
	}
	mcpJSON, mErr := json.MarshalIndent(mcpFile{MCPServers: servers}, "", "  ")
	if mErr != nil {
		cleanup()
		return nil, fmt.Errorf("marshaling mcp.json: %w", mErr)
	}
	if wErr := os.WriteFile(filepath.Join(tmpDir, "settings", "mcp.json"), mcpJSON, 0o600); wErr != nil {
		cleanup()
		return nil, fmt.Errorf("writing mcp.json: %w", wErr)
	}

	// agents/default.json — trimmed default agent including steering resources.
	agentJSON, aErr := json.MarshalIndent(map[string]any{
		"name":        "default",
		"description": "Moat sandbox agent",
		"tools":       []string{"*"},
		"resources": []string{
			"file://README.md",
			"file://AGENTS.md",
			"file://.kiro/steering/**/*.md",
			"file://~/.kiro/steering/**/*.md",
		},
		"includeMcpJson": true,
	}, "", "  ")
	if aErr != nil {
		cleanup()
		return nil, fmt.Errorf("marshaling default.json: %w", aErr)
	}
	if wErr := os.WriteFile(filepath.Join(tmpDir, "agents", "default.json"), agentJSON, 0o600); wErr != nil {
		cleanup()
		return nil, fmt.Errorf("writing default.json: %w", wErr)
	}

	// steering/moat-context.md — runtime context (only when non-empty).
	if opts.RuntimeContext != "" {
		if wErr := os.WriteFile(filepath.Join(tmpDir, "steering", "moat-context.md"), []byte(opts.RuntimeContext), 0o644); wErr != nil {
			cleanup()
			return nil, fmt.Errorf("writing steering context: %w", wErr)
		}
	}

	env := p.ContainerEnv(opts.Credential)
	env = append(env, "MOAT_KIRO_INIT="+KiroInitMountPath)

	return &provider.ContainerConfig{
		Env: env,
		Mounts: []provider.MountConfig{
			{Source: tmpDir, Target: KiroInitMountPath, ReadOnly: true},
		},
		StagingDir: tmpDir,
		Cleanup:    cleanup,
	}, nil
}
