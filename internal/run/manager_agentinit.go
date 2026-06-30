package run

// This file holds per-agent container staging (Claude/Codex/Gemini) used by
// Create. Each helper builds an agent's *provider.ContainerConfig via the
// provider interface. The caller resolves the provider (so its "not registered"
// error keeps its original rollback) and owns rolling back resources allocated
// before the call; these helpers create nothing that outlives a failed call.

import (
	"context"
	"fmt"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

// buildLocalMCPConfig converts an agent's local MCP server specs into the
// provider's LocalMCPServerConfig map, resolving each spec's optional grant
// into an injected env placeholder. agentName prefixes validation errors
// (e.g. "codex", "gemini"). Returns nil when there are no specs.
func buildLocalMCPConfig(agentName string, specs map[string]config.MCPServerSpec, grants []string) (map[string]provider.LocalMCPServerConfig, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	out := make(map[string]provider.LocalMCPServerConfig)
	for name, spec := range specs {
		env := spec.Env
		if spec.Grant != "" {
			v, ok := grantToEnvVar(spec.Grant)
			if !ok {
				return nil, fmt.Errorf("%s.mcp.%s: unknown grant %q (supported: github, openai, anthropic, gemini)", agentName, name, spec.Grant)
			}
			if !hasGrant(grants, spec.Grant) {
				return nil, fmt.Errorf("%s.mcp.%s: grant %q not declared in top-level grants list — add 'grants: [%s]' to agent.yaml", agentName, name, spec.Grant, spec.Grant)
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
		out[name] = provider.LocalMCPServerConfig{
			Command: spec.Command,
			Args:    spec.Args,
			Env:     env,
			Cwd:     spec.Cwd,
		}
	}
	return out, nil
}

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

	// Build local MCP server config from codex.mcp entries.
	var codexLocalMCP map[string]provider.LocalMCPServerConfig
	if opts.Config != nil {
		var err error
		codexLocalMCP, err = buildLocalMCPConfig("codex", opts.Config.Codex.MCP, opts.Config.Grants)
		if err != nil {
			return nil, err
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

// setupGeminiStaging builds the Gemini container config (Gemini auth + local MCP
// servers) via the provider interface.
func (m *Manager) setupGeminiStaging(ctx context.Context, geminiProvider provider.AgentProvider, opts Options, needsGeminiInit bool, containerHome, renderedContext string, openCredStore func() (*credential.FileStore, error)) (*provider.ContainerConfig, error) {
	// Get Gemini credential for PrepareContainer
	var geminiCred *provider.Credential
	if needsGeminiInit {
		if store, storeErr := openCredStore(); storeErr == nil {
			if cred, err := store.Get(credential.ProviderGemini); err == nil {
				geminiCred = provider.FromLegacy(cred)
			}
		}
	}

	// Build local MCP server config from gemini.mcp entries.
	var geminiLocalMCP map[string]provider.LocalMCPServerConfig
	if opts.Config != nil {
		var err error
		geminiLocalMCP, err = buildLocalMCPConfig("gemini", opts.Config.Gemini.MCP, opts.Config.Grants)
		if err != nil {
			return nil, err
		}
	}

	geminiConfig, prepErr := geminiProvider.PrepareContainer(ctx, provider.PrepareOpts{
		Credential:      geminiCred,
		ContainerHome:   containerHome,
		RuntimeContext:  renderedContext,
		LocalMCPServers: geminiLocalMCP,
	})
	if prepErr != nil {
		return nil, fmt.Errorf("preparing Gemini container config: %w", prepErr)
	}
	return geminiConfig, nil
}
