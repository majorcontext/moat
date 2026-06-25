package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
)

func TestWorkspaceVolumeName(t *testing.T) {
	if got := WorkspaceVolumeName("run_a1b2c3"); got != "moat-ws-run_a1b2c3" {
		t.Fatalf("got %q", got)
	}
}

func TestVolumeWorkspaceMounts(t *testing.T) {
	mounts := VolumeWorkspaceMounts("/host/ws", "moat-ws-x")
	if len(mounts) != 2 {
		t.Fatalf("want 2 mounts, got %d", len(mounts))
	}
	if mounts[0].Source != "/host/ws" || mounts[0].Target != stagingPath || !mounts[0].ReadOnly {
		t.Errorf("staging mount wrong: %+v", mounts[0])
	}
	if !mounts[1].Volume || mounts[1].Source != "moat-ws-x" || mounts[1].Target != "/workspace" {
		t.Errorf("volume mount wrong: %+v", mounts[1])
	}
}

func TestGuardVolumeWorkspaceRejectsWorktree(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere/.git/worktrees/x"), 0644); err != nil {
		t.Fatal(err)
	}
	err := GuardVolumeWorkspace(dir, container.RuntimeDocker)
	if err == nil || !strings.Contains(err.Error(), "worktree") {
		t.Fatalf("want worktree rejection, got %v", err)
	}
}

func TestGuardVolumeWorkspaceRejectsApple(t *testing.T) {
	err := GuardVolumeWorkspace(t.TempDir(), container.RuntimeApple)
	if err == nil || !strings.Contains(err.Error(), "Docker") {
		t.Fatalf("want Apple rejection, got %v", err)
	}
}

func TestGuardVolumeWorkspaceAllowsNormalRepoOnDocker(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := GuardVolumeWorkspace(dir, container.RuntimeDocker); err != nil {
		t.Fatalf("normal repo on docker should pass: %v", err)
	}
}

func TestConfigHasExplicitWorkspaceMount(t *testing.T) {
	yes := &config.Config{Mounts: []config.MountEntry{{Target: "/workspace"}}}
	if !ConfigHasExplicitWorkspaceMount(yes) {
		t.Error("should detect explicit /workspace mount")
	}
	no := &config.Config{Mounts: []config.MountEntry{{Target: "/data"}}}
	if ConfigHasExplicitWorkspaceMount(no) {
		t.Error("should not flag a non-/workspace mount")
	}
	if ConfigHasExplicitWorkspaceMount(nil) {
		t.Error("nil config should be false")
	}
}

func TestWorkspaceExcludes(t *testing.T) {
	// Patterns are "./"-prefixed (so they match the "./"-rooted tar members,
	// including nested patterns like "dist/sub") and newline-delimited (GNU tar
	// 1.34's --null --exclude-from only honors the first record).
	cfg := &config.Config{Mounts: []config.MountEntry{
		{Target: "/workspace", Exclude: []string{"node_modules", "dist/sub"}},
	}}
	got := workspaceExcludes(cfg)
	if got != "./node_modules\n./dist/sub" {
		t.Fatalf("got %q", got)
	}
	if workspaceExcludes(nil) != "" {
		t.Fatalf("nil config should give empty string")
	}
	// No /workspace mount → empty (no stray "./" emitted).
	noWS := &config.Config{Mounts: []config.MountEntry{{Target: "/data", Exclude: []string{"x"}}}}
	if got := workspaceExcludes(noWS); got != "" {
		t.Fatalf("non-/workspace mount should give empty string, got %q", got)
	}
}

func TestCheckDestroyAllowed(t *testing.T) {
	// volume mode, no extraction snapshot, not forced → blocked
	if err := CheckDestroyAllowed("volume", false, false); err == nil {
		t.Error("destroy should be blocked to prevent data loss")
	}
	// forced → allowed
	if err := CheckDestroyAllowed("volume", false, true); err != nil {
		t.Errorf("forced destroy should be allowed: %v", err)
	}
	// has an extraction snapshot → allowed
	if err := CheckDestroyAllowed("volume", true, false); err != nil {
		t.Errorf("destroy with snapshot should be allowed: %v", err)
	}
	// bind mode → always allowed
	if err := CheckDestroyAllowed("bind", false, false); err != nil {
		t.Errorf("bind destroy unaffected: %v", err)
	}
}
