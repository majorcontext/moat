package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

// CodexInitMountPath is where the staging directory is mounted in containers.
const CodexInitMountPath = "/moat/codex-init"

// PrepareContainer sets up staging directories and config files for Codex CLI.
// It creates a staging directory with auth.json and config.toml files
// that will be copied to ~/.codex at container startup by moat-init.
func (p *Provider) PrepareContainer(ctx context.Context, opts provider.PrepareOpts) (*provider.ContainerConfig, error) {
	// Create temporary directory for staging
	tmpDir, err := os.MkdirTemp("", "moat-codex-staging-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}

	cleanupFn := func() {
		os.RemoveAll(tmpDir)
	}

	authKind := ResolveAuthKind(opts.Credential)

	// Populate staging directory with auth.json
	if err := PopulateStagingDir(opts.Credential, tmpDir); err != nil {
		cleanupFn()
		return nil, fmt.Errorf("populating staging dir: %w", err)
	}

	// Write Codex config.toml, including any MCP servers. Codex reads MCP
	// servers from config.toml only, so both the remote (relay) and local
	// (child process) servers go into the same [mcp_servers] table.
	codexCfg := NewConfig(opts.CodexRequireApproval)
	if authKind != AuthSubscription {
		// Only subscription auth pins the ChatGPT origin. Leaving it unset lets
		// Codex use its own default for every other mode.
		codexCfg.ChatGPTBaseURL = ""
	}
	mcpServers, mcpErr := buildMCPServers(opts)
	if mcpErr != nil {
		cleanupFn()
		return nil, mcpErr
	}
	codexCfg.MCPServers = mcpServers
	if err := WriteCodexConfig(tmpDir, codexCfg); err != nil {
		cleanupFn()
		return nil, fmt.Errorf("writing codex config: %w", err)
	}

	// Write runtime context file if provided
	if opts.RuntimeContext != "" {
		if err := os.WriteFile(filepath.Join(tmpDir, "AGENTS.md"), []byte(opts.RuntimeContext), 0o644); err != nil {
			cleanupFn()
			return nil, fmt.Errorf("writing context file: %w", err)
		}
	}
	if opts.ScopeOpenAIKeyToShell {
		if err := os.WriteFile(filepath.Join(tmpDir, OpenAIShellEnvFileName), []byte(RenderOpenAIShellEnv()), 0o600); err != nil {
			cleanupFn()
			return nil, fmt.Errorf("writing OpenAI shell environment: %w", err)
		}
	}

	// Build container environment
	// Include credential env vars plus the init mount path for moat-init script
	env := p.ContainerEnv(opts.Credential)
	env = append(env, "MOAT_CODEX_INIT="+CodexInitMountPath)
	if authKind == AuthSubscription {
		// The synthetic auth file only works against the Codex versions the
		// adapter was verified for, so moat-init re-checks the executable that
		// is actually installed. Other modes must not be gated on it.
		env = append(env, "MOAT_CODEX_SUBSCRIPTION_AUTH=1")
	}
	if opts.ScopeOpenAIKeyToShell {
		env = append(env, "BASH_ENV="+OpenAIShellEnvPath)
	}

	// Build mounts - staging directory for init
	mounts := []provider.MountConfig{
		{
			Source:   tmpDir,
			Target:   CodexInitMountPath,
			ReadOnly: true,
		},
	}

	return &provider.ContainerConfig{
		Env:        env,
		Mounts:     mounts,
		StagingDir: tmpDir,
		Cleanup:    cleanupFn,
	}, nil
}

// buildMCPServers merges the remote (proxy-relay) and local (child process)
// MCP servers into a single [mcp_servers] table. Remote servers become
// streamable HTTP entries (url + http_headers); local ones become stdio
// entries (command/args/env/cwd).
//
// Names must be unique across both kinds because they share one table -
// silently letting one overwrite the other would drop a configured server.
func buildMCPServers(opts provider.PrepareOpts) (map[string]MCPServer, error) {
	if len(opts.MCPServers) == 0 && len(opts.LocalMCPServers) == 0 {
		return nil, nil
	}

	servers := make(map[string]MCPServer, len(opts.MCPServers)+len(opts.LocalMCPServers))
	for name, cfg := range opts.MCPServers {
		servers[name] = MCPServer{
			URL:         cfg.URL,
			HTTPHeaders: cfg.Headers,
		}
	}
	for name, cfg := range opts.LocalMCPServers {
		if _, exists := servers[name]; exists {
			return nil, fmt.Errorf("mcp server name %q is used by both a remote and a local server — names must be unique", name)
		}
		servers[name] = MCPServer{
			Command: cfg.Command,
			Args:    cfg.Args,
			Env:     cfg.Env,
			Cwd:     cfg.Cwd,
		}
	}
	return servers, nil
}

// AuthKind is the credential a run stages for Codex. Every auth-dependent
// decision in this package routes through it so staging, config, and the
// container's version gate cannot disagree about which mode is in effect.
type AuthKind int

const (
	// AuthNone means no Codex credential was granted. Codex is left
	// unauthenticated so it prompts for login, rather than being handed a
	// fabricated subscription the proxy will never honor.
	AuthNone AuthKind = iota
	// AuthAPIKey is a stored OpenAI API key, injected on api.openai.com.
	AuthAPIKey
	// AuthSubscription is a stored ChatGPT subscription, injected as a scoped
	// credential bundle on the ChatGPT Codex backend.
	AuthSubscription
)

// ResolveAuthKind maps a resolved credential to the staging mode it implies.
// An unrecognized provider is treated as AuthNone: guessing wrong here writes
// an auth file that claims an authentication Codex does not actually have.
func ResolveAuthKind(cred *provider.Credential) AuthKind {
	if cred == nil {
		return AuthNone
	}
	switch cred.Provider {
	case string(credential.ProviderCodexSubscription):
		return AuthSubscription
	case string(credential.ProviderOpenAI):
		return AuthAPIKey
	default:
		return AuthNone
	}
}

// PopulateStagingDir populates the Codex staging directory with auth configuration.
//
// Files added:
//   - auth.json (synthetic subscription tokens or a placeholder API key), or
//     nothing at all when no credential was granted
//
// SECURITY: The real token is NEVER written to the container filesystem.
// Authentication is handled by the TLS-intercepting proxy at the network layer.
func PopulateStagingDir(cred *provider.Credential, stagingDir string) error {
	var authFile map[string]any
	switch ResolveAuthKind(cred) {
	case AuthNone:
		// Write no auth.json. moat-init copies it only when present, so Codex
		// starts logged out and says so, instead of reporting a synthetic
		// login and then failing every request with an opaque 401.
		return nil
	case AuthAPIKey:
		authFile = map[string]any{"OPENAI_API_KEY": OpenAIAPIKeyPlaceholder}
	case AuthSubscription:
		authFile = map[string]any{
			"auth_mode":      "chatgptAuthTokens",
			"OPENAI_API_KEY": nil,
			"tokens": map[string]string{
				"id_token":      credential.GenerateIDTokenPlaceholder(syntheticAccountID),
				"access_token":  credential.GenerateAccessTokenPlaceholder(syntheticAccountID),
				"refresh_token": credential.ProxyInjectedPlaceholder,
				"account_id":    syntheticAccountID,
			},
			"last_refresh": time.Now().UTC().Format(time.RFC3339),
		}
	}

	authJSON, err := json.MarshalIndent(authFile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling auth file: %w", err)
	}

	if writeErr := os.WriteFile(filepath.Join(stagingDir, "auth.json"), authJSON, 0o600); writeErr != nil {
		return fmt.Errorf("writing auth file: %w", writeErr)
	}

	return nil
}
