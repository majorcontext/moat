package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/provider"
)

// PrepareContainer sets up staging directories and config files for Claude Code.
// It creates the necessary files that will be copied into the container at startup.
//
// If opts.HostConfig is nil, this method reads the host's ~/.claude.json automatically.
//
// This method works with both OAuth tokens and API keys. The credential type
// determines which environment variable placeholder is set.
func (p *OAuthProvider) PrepareContainer(ctx context.Context, opts provider.PrepareOpts) (*provider.ContainerConfig, error) {
	// Create a temporary staging directory
	tmpDir, err := os.MkdirTemp("", "moat-claude-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp directory: %w", err)
	}

	// Ensure proper permissions
	if err := os.Chmod(tmpDir, 0700); err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("setting permissions on staging dir: %w", err)
	}

	success := false
	defer func() {
		if !success {
			os.RemoveAll(tmpDir)
		}
	}()

	// Write credentials file for OAuth tokens
	if opts.Credential != nil {
		if err := WriteCredentialsFile(opts.Credential, tmpDir); err != nil {
			return nil, fmt.Errorf("writing credentials file: %w", err)
		}
	}

	// Convert MCP servers to Claude's format
	var mcpServers map[string]MCPServerForContainer
	if len(opts.MCPServers) > 0 {
		mcpServers = make(map[string]MCPServerForContainer)
		for name, cfg := range opts.MCPServers {
			mcpServers[name] = MCPServerForContainer{
				Type:    "http",
				URL:     cfg.URL,
				Headers: cfg.Headers,
			}
		}
	}

	// Get host config - use provided or read from host's ~/.claude.json
	var hostConfig map[string]any
	if opts.HostConfig != nil {
		hostConfig = opts.HostConfig
	} else {
		// Read host config automatically
		if hostHome, err := os.UserHomeDir(); err == nil {
			hostConfig, _ = ReadHostConfig(filepath.Join(hostHome, ".claude.json"))
			// Ignore errors - missing host config is OK
		}
	}

	// Write .claude.json config
	if err := WriteClaudeConfig(tmpDir, mcpServers, hostConfig); err != nil {
		return nil, fmt.Errorf("writing claude config: %w", err)
	}

	// Build mounts
	mounts := []provider.MountConfig{
		{
			Source:   tmpDir,
			Target:   ClaudeInitMountPath,
			ReadOnly: true,
		},
	}

	// Build environment variables based on credential type.
	// PrepareContainer can be called with either OAuth or API key credentials.
	env := containerEnvForCredential(opts.Credential)
	env = append(env, "MOAT_CLAUDE_INIT="+ClaudeInitMountPath)

	success = true
	return &provider.ContainerConfig{
		Env:        env,
		Mounts:     mounts,
		StagingDir: tmpDir,
		Cleanup: func() {
			os.RemoveAll(tmpDir)
		},
	}, nil
}

// containerEnvForCredential returns the correct environment variable based on
// the credential's provider identity. OAuth credentials (provider "claude") get
// CLAUDE_CODE_OAUTH_TOKEN, API key credentials (provider "anthropic") get
// ANTHROPIC_API_KEY. Both use placeholder values — real credentials are injected
// by the proxy at the network layer.
func containerEnvForCredential(cred *provider.Credential) []string {
	if cred != nil && cred.Provider == "claude" {
		return []string{"CLAUDE_CODE_OAUTH_TOKEN=" + ProxyInjectedPlaceholder}
	}
	return []string{"ANTHROPIC_API_KEY=" + ProxyInjectedPlaceholder}
}
