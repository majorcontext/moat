package run

// This file holds per-agent container staging (Claude/Codex/Gemini) used by
// Create. Each helper builds an agent's *provider.ContainerConfig via the
// provider interface. The caller resolves the provider (so its "not registered"
// error keeps its original rollback) and owns rolling back resources allocated
// before the call; these helpers create nothing that outlives a failed call.

import (
	"context"
	"fmt"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

// setupCodexStaging builds the Codex container config (OpenAI auth + local MCP
// servers) via the provider interface. openCredStore must be non-nil when
// needsCodexInit is true (Create always passes a real closure).
func (m *Manager) setupCodexStaging(ctx context.Context, codexProvider provider.AgentProvider, opts Options, needsCodexInit bool, containerHome, renderedContext string, openCredStore func() (*credential.FileStore, error)) (*provider.ContainerConfig, error) {
	// Get Codex credential for PrepareContainer
	var codexCred *provider.Credential
	if needsCodexInit {
		if store, storeErr := openCredStore(); storeErr == nil {
			if cred, err := store.Get(credential.ProviderOpenAI); err == nil {
				codexCred = provider.FromLegacy(cred)
			}
		}
	}

	// Build local MCP server config from codex.mcp entries
	var codexLocalMCP map[string]provider.LocalMCPServerConfig
	if opts.Config != nil && len(opts.Config.Codex.MCP) > 0 {
		codexLocalMCP = make(map[string]provider.LocalMCPServerConfig)
		for name, spec := range opts.Config.Codex.MCP {
			env := spec.Env
			if spec.Grant != "" {
				v, ok := grantToEnvVar(spec.Grant)
				if !ok {
					return nil, fmt.Errorf("codex.mcp.%s: unknown grant %q (supported: github, openai, anthropic, gemini)", name, spec.Grant)
				}
				if !hasGrant(opts.Config.Grants, spec.Grant) {
					return nil, fmt.Errorf("codex.mcp.%s: grant %q not declared in top-level grants list — add 'grants: [%s]' to agent.yaml", name, spec.Grant, spec.Grant)
				}
				if env == nil {
					env = make(map[string]string)
				} else {
					// Copy to avoid mutating the original config
					envCopy := make(map[string]string, len(env)+1)
					for k, v := range env {
						envCopy[k] = v
					}
					env = envCopy
				}
				env[v] = grantToPlaceholder(spec.Grant)
			}
			codexLocalMCP[name] = provider.LocalMCPServerConfig{
				Command: spec.Command,
				Args:    spec.Args,
				Env:     env,
				Cwd:     spec.Cwd,
			}
		}
	}

	codexConfig, prepErr := codexProvider.PrepareContainer(ctx, provider.PrepareOpts{
		Credential:      codexCred,
		ContainerHome:   containerHome,
		RuntimeContext:  renderedContext,
		LocalMCPServers: codexLocalMCP,
	})
	if prepErr != nil {
		return nil, fmt.Errorf("preparing Codex container config: %w", prepErr)
	}
	return codexConfig, nil
}
