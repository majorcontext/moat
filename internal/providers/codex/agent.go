package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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

	// Populate staging directory with auth.json
	if err := PopulateStagingDir(opts.Credential, tmpDir); err != nil {
		cleanupFn()
		return nil, fmt.Errorf("populating staging dir: %w", err)
	}

	// Write Codex config.toml
	if err := WriteCodexConfig(tmpDir); err != nil {
		cleanupFn()
		return nil, fmt.Errorf("writing codex config: %w", err)
	}

	// Build container environment
	// Include credential env vars plus the init mount path for moat-init script
	env := p.ContainerEnv(opts.Credential)
	env = append(env, "MOAT_CODEX_INIT="+CodexInitMountPath)

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

// PopulateStagingDir populates the Codex staging directory with auth configuration.
//
// Files added:
//   - auth.json (placeholder API key - real auth is via proxy)
//
// SECURITY: The real token is NEVER written to the container filesystem.
// Authentication is handled by the TLS-intercepting proxy at the network layer.
func PopulateStagingDir(cred *provider.Credential, stagingDir string) error {
	// API key - use a placeholder that looks like a valid API key
	// This bypasses local format validation in Codex CLI.
	// The proxy will inject the real key in the Authorization header.
	authFile := map[string]string{
		"OPENAI_API_KEY": OpenAIAPIKeyPlaceholder,
	}

	authJSON, err := json.MarshalIndent(authFile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling auth file: %w", err)
	}

	if writeErr := os.WriteFile(filepath.Join(stagingDir, "auth.json"), authJSON, 0600); writeErr != nil {
		return fmt.Errorf("writing auth file: %w", writeErr)
	}

	return nil
}
