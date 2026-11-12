package run

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/container"
)

// codexSessionMount persists transcripts only. Codex reconstructs its private
// index from rollouts; sharing the whole home would expose auth and databases.
func codexSessionMount(hostHome, workspace, containerHome string, shared bool) (container.MountConfig, error) {
	hostDir := codexSessionsHostDir(hostHome, workspace, shared)
	if hostDir == "" || !filepath.IsAbs(hostDir) || !filepath.IsAbs(containerHome) {
		return container.MountConfig{}, fmt.Errorf("cannot persist Codex sessions: check the workspace path and home directories; host HOME and the container home must be absolute")
	}
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		return container.MountConfig{}, fmt.Errorf("creating Codex session directory %s: %w", hostDir, err)
	}
	return container.MountConfig{Source: hostDir, Target: filepath.Join(containerHome, ".codex", "sessions")}, nil
}
