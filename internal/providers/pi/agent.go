package pi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/provider"
)

// PrepareContainer stages the Pi runtime-context file and returns the mount +
// env needed to inject it.
//
// The context is injected into Pi's system prompt at launch via
// --append-system-prompt (see cli.go), so it augments rather than replaces the
// user's own AGENTS.md / CLAUDE.md. No credential is staged here: the real API
// key is injected by the proxy via the backend's grant provider.
func (p *Provider) PrepareContainer(ctx context.Context, opts provider.PrepareOpts) (*provider.ContainerConfig, error) {
	tmpDir, err := os.MkdirTemp("", "moat-pi-staging-*")
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

	// PI_OFFLINE suppresses Pi's startup catalog fetch; it does NOT block the
	// inference call. It also turns off extension model refreshes, which is
	// where LunaRoute's extension gets its models, so it is omitted when the
	// run needs that sync. The context file is read directly from the mount
	// below via --append-system-prompt, so (unlike the other agents) no
	// MOAT_*_INIT env is needed — moat-init has no Pi copy step.
	var env []string
	if !opts.PiModelCatalogSync {
		env = append(env, "PI_OFFLINE=1")
	}
	mounts := []provider.MountConfig{
		{Source: tmpDir, Target: PiInitMountPath, ReadOnly: true},
	}
	return &provider.ContainerConfig{
		Env:        env,
		Mounts:     mounts,
		StagingDir: tmpDir,
		Cleanup:    cleanupFn,
	}, nil
}
