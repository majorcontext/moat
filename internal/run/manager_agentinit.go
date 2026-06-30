package run

// This file holds per-agent container staging (Claude/Codex/Gemini) used by
// Create. Each helper builds an agent's *provider.ContainerConfig via the
// provider interface. The caller resolves the provider (so its "not registered"
// error keeps its original rollback) and owns rolling back resources allocated
// before the call; these helpers create nothing that outlives a failed call.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/providers/claude"
	"github.com/majorcontext/moat/internal/ui"
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

// setupClaudeStaging builds the Claude container config via the provider
// interface: it assembles the proxy-relay MCP server config, resolves the
// Claude (or, for back-compat, Anthropic) credential, prepares the container,
// and writes settings.json into the staging dir (pinning the renderer and, for
// bypass runs, persisting bypass-permissions mode). claude.mcp does not support
// grants, so its local-MCP build is a plain conversion. openCredStore must be
// non-nil when needsClaudeInit is true; claudeSettings may be nil.
func (m *Manager) setupClaudeStaging(ctx context.Context, claudeProvider provider.AgentProvider, opts Options, r *Run, needsClaudeInit, hasPlugins, hasClaudeCode bool, claudeSettings *claude.Settings, containerHome, renderedContext string, openCredStore func() (*credential.FileStore, error)) (*provider.ContainerConfig, error) {
	// Build MCP server configuration for .claude.json
	// Use proxy relay URLs instead of direct MCP server URLs to work around
	// Claude Code's MCP client not respecting HTTP_PROXY environment variables.
	// This also bridges host-local MCP servers (localhost/127.0.0.1) which
	// the container cannot reach directly.
	mcpServers := make(map[string]provider.MCPServerConfig)
	if opts.Config != nil && len(opts.Config.MCP) > 0 {
		// Use the synthetic proxy host (in NO_PROXY) so the MCP client
		// connects directly and the proxy strips the per-run token via
		// handleDirectMCPRelay. GetHostAddress is not in NO_PROXY, so it would
		// route through the CONNECT tunnel to the wrong handler → 404.
		proxyAddr := fmt.Sprintf("%s:%d", syntheticProxyHost, r.ProxyPort)
		for _, mcp := range opts.Config.MCP {
			relayURL := fmt.Sprintf("http://%s/mcp/%s/%s", proxyAddr, r.ProxyAuthToken, mcp.Name)
			mcpCfg := provider.MCPServerConfig{
				URL: relayURL,
			}
			if mcp.Auth != nil {
				mcpCfg.Headers = map[string]string{
					mcp.Auth.Header: "moat-stub-" + mcp.Auth.Grant,
				}
			}
			mcpServers[mcp.Name] = mcpCfg
		}
	}

	// Get Claude credential for PrepareContainer
	// Preference: claude > anthropic (for backward compatibility)
	var claudeCred *provider.Credential
	if needsClaudeInit {
		if store, storeErr := openCredStore(); storeErr == nil {
			// Try claude first, fall back to anthropic
			cred, err := store.Get(credential.ProviderClaude)
			if err != nil {
				cred, err = store.Get(credential.ProviderAnthropic)
			}
			if err == nil {
				claudeCred = provider.FromLegacy(cred)
			}
		}
	}

	// Build local MCP server config from claude.mcp entries (claude.mcp does
	// not support grants, so there is no grant->env resolution here).
	var claudeLocalMCP map[string]provider.LocalMCPServerConfig
	if opts.Config != nil && len(opts.Config.Claude.MCP) > 0 {
		claudeLocalMCP = make(map[string]provider.LocalMCPServerConfig)
		for name, spec := range opts.Config.Claude.MCP {
			claudeLocalMCP[name] = provider.LocalMCPServerConfig{
				Command: spec.Command,
				Args:    spec.Args,
				Env:     spec.Env,
				Cwd:     spec.Cwd,
			}
		}
	}

	// Call provider to prepare container config
	var claudeSubType, claudeRateTier string
	if opts.Config != nil {
		claudeSubType = opts.Config.Claude.SubscriptionType
		claudeRateTier = opts.Config.Claude.RateLimitTier
	}
	claudeConfig, prepErr := claudeProvider.PrepareContainer(ctx, provider.PrepareOpts{
		Credential:       claudeCred,
		ContainerHome:    containerHome,
		MCPServers:       mcpServers,
		RuntimeContext:   renderedContext,
		LocalMCPServers:  claudeLocalMCP,
		SubscriptionType: claudeSubType,
		RateLimitTier:    claudeRateTier,
		// HostConfig is read automatically by the provider if nil
	})
	if prepErr != nil {
		return nil, fmt.Errorf("preparing Claude container config: %w", prepErr)
	}

	// Write settings.json to suppress startup prompts, pin the renderer,
	// persist bypass-permissions mode, and configure plugins.
	// moat-init.sh copies $MOAT_CLAUDE_INIT/settings.json to ~/.claude/settings.json.
	skipPrompt := opts.Config != nil && opts.Config.Claude.SkipPermissionsPrompt
	if hasPlugins || skipPrompt || hasClaudeCode {
		if claudeSettings == nil {
			claudeSettings = &claude.Settings{}
		}
		// Pin the renderer and (for bypass runs) persist bypass mode so
		// Claude Code's fullscreen-renderer re-exec can't silently drop the
		// --dangerously-skip-permissions flag. See Settings.ApplyRunPolicy.
		if policyErr := claudeSettings.ApplyRunPolicy(hasClaudeCode, skipPrompt); policyErr != nil {
			ui.Warnf("Failed to persist bypass-permissions mode in settings.json (check the 'permissions' value in ~/.moat/claude/settings.json): %v", policyErr)
		}
		settingsPath := filepath.Join(claudeConfig.StagingDir, "settings.json")
		settingsJSON, jsonErr := json.MarshalIndent(claudeSettings, "", "  ")
		if jsonErr != nil {
			// MarshalIndent cannot fail for Settings (no channels, funcs, or cycles);
			// log.Warn for defense-in-depth only.
			log.Warn("failed to marshal settings.json", "error", jsonErr)
		} else if writeErr := os.WriteFile(settingsPath, settingsJSON, 0o644); writeErr != nil {
			ui.Warnf("Failed to write Claude settings to container: %v", writeErr)
		} else {
			log.Debug("wrote settings.json to staging dir",
				"plugins", len(claudeSettings.EnabledPlugins),
				"marketplaces", len(claudeSettings.ExtraKnownMarketplaces))
		}
	}

	return claudeConfig, nil
}
