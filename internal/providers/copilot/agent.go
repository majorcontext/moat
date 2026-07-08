package copilot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/provider"
)

// PrepareContainer stages Copilot config and runtime context.
func (p *Provider) PrepareContainer(ctx context.Context, opts provider.PrepareOpts) (*provider.ContainerConfig, error) {
	tmpDir, err := os.MkdirTemp("", "moat-copilot-staging-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	cleanupFn := func() { os.RemoveAll(tmpDir) }

	if opts.RuntimeContext != "" {
		if writeErr := os.WriteFile(filepath.Join(tmpDir, ContextFileName), []byte(opts.RuntimeContext), 0o644); writeErr != nil {
			cleanupFn()
			return nil, fmt.Errorf("writing context file: %w", writeErr)
		}
	}

	// Trust /workspace up front so first-run prompts don't block headless runs.
	configJSON := []byte(`{"trustedFolders":["/workspace"]}` + "\n")
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), configJSON, 0o600); err != nil {
		cleanupFn()
		return nil, fmt.Errorf("writing copilot config: %w", err)
	}

	env := p.ContainerEnv(opts.Credential)
	env = append(env,
		"MOAT_COPILOT_INIT="+CopilotInitMountPath,
		"COPILOT_CUSTOM_INSTRUCTIONS_DIRS="+CopilotInitMountPath,
		"COPILOT_AUTO_UPDATE=false",
	)

	return &provider.ContainerConfig{
		Env: env,
		Mounts: []provider.MountConfig{{
			Source:   tmpDir,
			Target:   CopilotInitMountPath,
			ReadOnly: true,
		}},
		StagingDir: tmpDir,
		Cleanup:    cleanupFn,
	}, nil
}
