package run

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/daemon"
)

// stagingPath is where the host tree is bind-mounted read-only during copy-in.
const stagingPath = "/mnt/host-workspace"

// WorkspaceVolumeName derives the per-run Docker volume name. The prefix is
// defined in internal/daemon (the GC consumer that must match it and cannot
// import run without a cycle) so there is a single source of truth.
func WorkspaceVolumeName(runID string) string { return daemon.WorkspaceVolumePrefix + runID }

// VolumeWorkspaceMounts returns the staging bind (host tree, read-only) and the
// named-volume mount at /workspace for volume mode.
func VolumeWorkspaceMounts(hostWorkspace, volumeName string) []container.MountConfig {
	return []container.MountConfig{
		{Source: hostWorkspace, Target: stagingPath, ReadOnly: true},
		{Volume: true, Source: volumeName, Target: "/workspace"},
	}
}

// GuardVolumeWorkspace rejects volume mode when it cannot work: non-Docker
// runtime, or a git worktree/submodule (.git is a file, not a directory).
func GuardVolumeWorkspace(hostWorkspace string, rt container.RuntimeType) error {
	if rt != container.RuntimeDocker {
		return fmt.Errorf("volume mode requires the Docker runtime; set workspace.mode: bind or run with --runtime docker")
	}
	if info, err := os.Lstat(filepath.Join(hostWorkspace, ".git")); err == nil && !info.IsDir() {
		return fmt.Errorf("volume mode does not support git worktrees or submodules (.git is a file at %s/.git); use the main checkout or workspace.mode: bind", hostWorkspace)
	}
	return nil
}

// CheckDestroyAllowed blocks destroying a volume-mode run that has no extraction
// snapshot, unless force is set. The volume is the only copy of the agent's work;
// removing it without an extraction snapshot loses everything, including in-volume
// commits. Bind-mode runs are unaffected (the host tree persists).
func CheckDestroyAllowed(workspaceMode string, hasExtractionSnapshot, force bool) error {
	if workspaceMode == string(config.WorkspaceModeVolume) && !hasExtractionSnapshot && !force {
		return fmt.Errorf("this volume-mode run has no extraction snapshot; destroying it deletes the workspace volume and loses all agent changes.\n" +
			"Capture your work first: `moat snapshot <run>` then `moat snapshot restore <run> --to <dir>`, or pass --force to destroy anyway")
	}
	return nil
}

// ConfigHasExplicitWorkspaceMount reports whether the config declares an
// explicit mount targeting /workspace (which conflicts with volume mode).
func ConfigHasExplicitWorkspaceMount(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	for _, me := range cfg.Mounts {
		if me.Target == "/workspace" {
			return true
		}
	}
	return false
}

// workspaceExcludeList returns the /workspace mount's exclude patterns, or nil.
func workspaceExcludeList(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	for _, me := range cfg.Mounts {
		if me.Target == "/workspace" {
			return me.Exclude
		}
	}
	return nil
}

// workspaceExcludes joins the /workspace mount's excludes into a newline-delimited
// string for MOAT_WORKSPACE_EXCLUDES (consumed by moat-init.sh --exclude-from).
//
// Each pattern is prefixed with "./" so it matches the "./"-rooted member names
// produced by `cd staging && tar -cf - .` (e.g. member "./dist/sub/" matches
// pattern "./dist/sub"). Without the prefix, single-component names like
// "node_modules" match by component but nested patterns like "dist/sub" silently
// fail to match the "./dist/sub/" member, leaving them copied into the volume.
//
// Records are newline-delimited (not NUL): GNU tar 1.34's `--null --exclude-from`
// only applies the first record, silently ignoring the rest, so the script reads
// excludes as plain newline-delimited patterns. Exclude patterns are validated at
// config load to a path-safe class (no whitespace/newlines), so newline is a safe
// delimiter.
func workspaceExcludes(cfg *config.Config) string {
	list := workspaceExcludeList(cfg)
	if len(list) == 0 {
		return ""
	}
	prefixed := make([]string, len(list))
	for i, p := range list {
		prefixed[i] = "./" + p
	}
	return strings.Join(prefixed, "\n")
}
