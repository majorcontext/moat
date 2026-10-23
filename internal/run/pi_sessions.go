package run

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
)

// piSessionMount mounts only transcripts, leaving Pi's settings, extensions,
// and auth inside the container. The entire sessions root is mounted so Pi's
// cwd-specific directories (including workspace subdirectories) all persist.
func piSessionMount(workspace, containerHome string) (container.MountConfig, error) {
	slug := workspaceSlug(workspace)
	if slug == "" || !filepath.IsAbs(containerHome) {
		return container.MountConfig{}, fmt.Errorf("pi.sync_logs requires a workspace and an absolute container home")
	}
	hostDir := filepath.Join(config.GlobalConfigDir(), "pi", "sessions", slug)
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		return container.MountConfig{}, fmt.Errorf("creating Pi session directory %s: %w", hostDir, err)
	}
	return container.MountConfig{
		Source: hostDir,
		Target: filepath.Join(containerHome, ".pi", "agent", "sessions"),
	}, nil
}
