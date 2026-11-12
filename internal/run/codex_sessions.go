package run

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/container"
)

// codexSessionMountForWorkspace fails closed when persistence is required but
// the host home directory cannot be resolved.
func codexSessionMountForWorkspace(workspace, containerHome string, shared bool) (container.MountConfig, error) {
	hostHome, err := os.UserHomeDir()
	if err != nil {
		return container.MountConfig{}, fmt.Errorf("cannot persist Codex sessions: resolving host home directory: %w", err)
	}
	return codexSessionMount(hostHome, workspace, containerHome, shared)
}

// codexSessionMount persists transcripts only. Codex reconstructs its private
// index from rollouts; sharing the whole home would expose auth and databases.
func codexSessionMount(hostHome, workspace, containerHome string, shared bool) (container.MountConfig, error) {
	hostDir := codexSessionsHostDir(hostHome, workspace, shared)
	if hostDir == "" || !filepath.IsAbs(hostDir) || !filepath.IsAbs(containerHome) {
		return container.MountConfig{}, fmt.Errorf("cannot persist Codex sessions: invalid paths: workspace=%q, hostDir=%q, containerHome=%q; the session directory and container home must be absolute; check HOME, MOAT_HOME, and the workspace path", workspace, hostDir, containerHome)
	}
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		return container.MountConfig{}, fmt.Errorf("creating Codex session directory %s: %w", hostDir, err)
	}
	return container.MountConfig{Source: hostDir, Target: filepath.Join(containerHome, ".codex", "sessions")}, nil
}
