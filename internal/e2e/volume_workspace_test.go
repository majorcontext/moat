//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/storage"
)

// runMoatCLI shells out to the real moat binary (MOAT_EXECUTABLE) with
// MOAT_NO_SANDBOX=1 so it uses Docker without gVisor. It returns the combined
// stdout+stderr and the process exit code (0 on success).
//
// Snapshot/restore/destroy have no in-process manager API, so the volume-mode
// e2e drives them through the CLI. This faithfully exercises the
// runtime-detection + VolumeExport + archive-backend restore code paths where
// the macOS-only bugs lived.
func runMoatCLI(t *testing.T, args ...string) (string, int) {
	t.Helper()
	bin := os.Getenv("MOAT_EXECUTABLE")
	if bin == "" {
		t.Skip("MOAT_EXECUTABLE not set (TestMain should set it)")
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "MOAT_NO_SANDBOX=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(out), exitErr.ExitCode()
	}
	// Failed to launch the process at all.
	t.Fatalf("running moat %v: %v\nOutput: %s", args, err, out)
	return string(out), -1
}

// gitCmd runs a git command in dir and fails the test on error.
func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\nOutput: %s", args, dir, err, out)
	}
}

// gitOut runs a git command in dir and returns trimmed stdout, failing on error.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return strings.TrimSpace(string(out))
}

// readBranchHead resolves the HEAD ref of a git repo at repoDir (which may be a
// snapshot-extracted tree, possibly with foreign uid/ownership) by reading the
// loose ref file directly. It parses .git/HEAD ("ref: refs/heads/X") to find the
// branch file rather than relying on `git log`/`git rev-parse`, which can be
// flaky across uid/ownership boundaries.
func readBranchHead(t *testing.T, repoDir string) string {
	t.Helper()
	gitDir := filepath.Join(repoDir, ".git")

	headBytes, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		t.Fatalf("reading %s/.git/HEAD: %v", repoDir, err)
	}
	head := strings.TrimSpace(string(headBytes))

	// Detached HEAD: the file contains the sha directly.
	if !strings.HasPrefix(head, "ref:") {
		return head
	}

	ref := strings.TrimSpace(strings.TrimPrefix(head, "ref:"))
	refBytes, err := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash(ref)))
	if err != nil {
		t.Fatalf("reading ref file %s in %s: %v", ref, gitDir, err)
	}
	return strings.TrimSpace(string(refBytes))
}

// initVolumeTestRepo creates a temp git repo with tracked and to-be-excluded
// files, commits a base, and returns the repo dir and its base HEAD sha.
func initVolumeTestRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()

	// Use -b main so the default branch is deterministic regardless of the
	// host git's init.defaultBranch setting.
	gitCmd(t, dir, "init", "-b", "main")
	gitCmd(t, dir, "config", "user.email", "test@e2e")
	gitCmd(t, dir, "config", "user.name", "e2e")

	writeFile(t, filepath.Join(dir, "README.md"), "hello\n")
	writeFile(t, filepath.Join(dir, "src", "main.go"), "package main\n")
	writeFile(t, filepath.Join(dir, "node_modules", "big"), "ignore me\n")
	writeFile(t, filepath.Join(dir, "dist", "sub", "x"), "excluded\n")
	writeFile(t, filepath.Join(dir, "dist", "keep", "y"), "kept\n")

	gitCmd(t, dir, "add", "README.md", "src/main.go", "dist/keep/y")
	gitCmd(t, dir, "commit", "-qm", "host base")

	head := gitOut(t, dir, "rev-parse", "HEAD")
	return dir, head
}

// writeFile writes content to path, creating parent directories.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

// dockerVolumeExists reports whether a Docker named volume exists.
func dockerVolumeExists(name string) bool {
	return exec.Command("docker", "volume", "inspect", name).Run() == nil
}

// findLogLine reports whether any captured log line contains substr.
func findLogLine(logs []storage.LogEntry, substr string) bool {
	for _, e := range logs {
		if strings.Contains(e.Line, substr) {
			return true
		}
	}
	return false
}

// extractLogValue returns the value following "key=" from the first log line
// that contains it (e.g. "AGENT_HEAD=abc123" -> "abc123"). Returns "" if absent.
func extractLogValue(logs []storage.LogEntry, key string) string {
	for _, e := range logs {
		idx := strings.Index(e.Line, key)
		if idx < 0 {
			continue
		}
		rest := e.Line[idx+len(key):]
		// Stop at any trailing whitespace.
		if sp := strings.IndexAny(rest, " \t\r\n"); sp >= 0 {
			rest = rest[:sp]
		}
		return strings.TrimSpace(rest)
	}
	return ""
}

// TestVolumeWorkspaceLifecycle exercises the full volume-mode workspace
// lifecycle: copy-in (with excludes, including a nested exclude), host
// protection, snapshot, restore --to with .git/agent-commit preservation, the
// in-place-restore block, and the destroy data-loss guard.
//
// requireDocker-gated so it runs in Linux CI and, on a Mac with Docker Desktop,
// also exercises the Docker-Desktop virtual-FS paths (VolumeExport copy,
// archive-backend restore) that had macOS-only bugs.
func TestVolumeWorkspaceLifecycle(t *testing.T) {
	requireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hostDir, baseHead := initVolumeTestRepo(t)

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	runName := "e2e-vol-ws"

	agentCmd := strings.Join([]string{
		`echo "WS_LIST=$(ls -A /workspace | tr '\n' ',')"`,
		`echo "HAS_README=$(test -f /workspace/README.md && echo yes || echo no)"`,
		`echo "HAS_GIT=$(test -d /workspace/.git && echo yes || echo no)"`,
		`echo "HAS_NODE_MODULES=$(test -e /workspace/node_modules && echo yes || echo no)"`,
		`echo "HAS_DIST_SUB=$(test -e /workspace/dist/sub && echo yes || echo no)"`,
		`echo "HAS_DIST_KEEP=$(test -e /workspace/dist/keep && echo yes || echo no)"`,
		`cd /workspace && git config user.email a@e && git config user.name a`,
		`echo AGENT_WAS_HERE > agent.txt && git add agent.txt && git commit -qm "agent commit in volume" && echo COMMIT_OK || echo COMMIT_FAILED`,
		`echo "AGENT_HEAD=$(git rev-parse HEAD)"`,
	}, "\n")

	r, err := mgr.Create(ctx, run.Options{
		Name:          runName,
		Workspace:     hostDir,
		WorkspaceMode: config.WorkspaceModeVolume,
		Config: &config.Config{
			Name:         runName,
			Agent:        "e2e-test",
			Dependencies: []string{"git"},
			Workspace:    config.WorkspaceConfig{Mode: "volume"},
			Mounts: []config.MountEntry{
				{
					Source:  ".",
					Target:  "/workspace",
					Exclude: []string{"node_modules", "dist/sub"},
				},
			},
		},
		Cmd: []string{"sh", "-c", agentCmd},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Force-clean the run + volume even if assertions below fail.
	t.Cleanup(func() {
		runMoatCLI(t, "destroy", "--force", r.ID)
		_ = exec.Command("docker", "volume", "rm", run.WorkspaceVolumeName(r.ID)).Run()
	})

	if err := mgr.Start(ctx, r.ID, run.StartOptions{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := mgr.Wait(ctx, r.ID); err != nil {
		t.Logf("Wait returned error: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	logs, err := store.ReadLogs(0, 200)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}

	// Assert copy-in populated the volume with tracked files and .git, and that
	// excludes (including the nested dist/sub) were honored while siblings stayed.
	checks := []struct {
		want string
		desc string
	}{
		{"HAS_README=yes", "README.md present in volume"},
		{"HAS_GIT=yes", ".git present in volume"},
		{"HAS_NODE_MODULES=no", "node_modules excluded"},
		{"HAS_DIST_SUB=no", "dist/sub nested-excluded"},
		{"HAS_DIST_KEEP=yes", "dist/keep retained (exclude is scoped)"},
		{"COMMIT_OK", "agent commit succeeded in volume"},
	}
	for _, c := range checks {
		if !findLogLine(logs, c.want) {
			t.Fatalf("expected %q (%s) in logs\nLogs:%s", c.want, c.desc, formatLogEntries(logs))
		}
	}

	agentHead := extractLogValue(logs, "AGENT_HEAD=")
	if agentHead == "" {
		t.Fatalf("could not extract AGENT_HEAD from logs\nLogs:%s", formatLogEntries(logs))
	}

	// Host protection: the agent's commit and file must NOT have touched the
	// host working tree.
	if _, statErr := os.Stat(filepath.Join(hostDir, "agent.txt")); !os.IsNotExist(statErr) {
		t.Errorf("host protection violated: agent.txt exists on host dir %s (err=%v)", hostDir, statErr)
	}
	if hostHead := gitOut(t, hostDir, "rev-parse", "HEAD"); hostHead != baseHead {
		t.Errorf("host protection violated: host HEAD changed from %s to %s", baseHead, hostHead)
	}

	// Snapshot via CLI (exercises VolumeExport + archive backend).
	if out, code := runMoatCLI(t, "snapshot", r.ID); code != 0 {
		t.Fatalf("moat snapshot exited %d\nOutput: %s", code, out)
	}

	// Restore via CLI to an extraction directory.
	extractDir := filepath.Join(t.TempDir(), "extract")
	if out, code := runMoatCLI(t, "snapshot", "restore", r.ID, "--to", extractDir); code != 0 {
		t.Fatalf("moat snapshot restore --to exited %d\nOutput: %s", code, out)
	}

	if _, statErr := os.Stat(filepath.Join(extractDir, "agent.txt")); statErr != nil {
		t.Errorf("extract missing agent.txt: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(extractDir, ".git")); statErr != nil {
		t.Errorf("extract missing .git: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(extractDir, "node_modules")); !os.IsNotExist(statErr) {
		t.Errorf("extract should not contain excluded node_modules (err=%v)", statErr)
	}

	// Robust agent-commit check: the extracted repo's branch HEAD must equal the
	// sha the agent committed inside the volume — proves the commit survived
	// snapshot -> restore without relying on git log.
	if got := readBranchHead(t, extractDir); got != agentHead {
		t.Errorf("extracted repo HEAD = %q, want agent commit %q", got, agentHead)
	}

	// In-place restore must be blocked for volume-mode runs.
	out, code := runMoatCLI(t, "snapshot", "restore", r.ID)
	if code == 0 {
		t.Errorf("in-place restore should fail for volume-mode run, but exited 0\nOutput: %s", out)
	}
	if !strings.Contains(out, "in-place restore is not allowed") {
		t.Errorf("in-place restore error should mention block reason\nOutput: %s", out)
	}

	// Destroy via CLI: a snapshot exists, so the guard passes and destroy succeeds.
	volName := run.WorkspaceVolumeName(r.ID)
	if out, code := runMoatCLI(t, "destroy", r.ID); code != 0 {
		t.Fatalf("moat destroy (with snapshot) exited %d\nOutput: %s", code, out)
	}
	if dockerVolumeExists(volName) {
		t.Errorf("workspace volume %s still exists after destroy", volName)
	}
}

// TestVolumeWorkspaceDestroyGuard verifies the destroy data-loss guard: a
// volume-mode run with no extraction snapshot refuses to be destroyed without
// --force, and the volume persists until --force is used.
func TestVolumeWorkspaceDestroyGuard(t *testing.T) {
	requireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hostDir, _ := initVolumeTestRepo(t)

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	runName := "e2e-vol-guard"

	r, err := mgr.Create(ctx, run.Options{
		Name:          runName,
		Workspace:     hostDir,
		WorkspaceMode: config.WorkspaceModeVolume,
		Config: &config.Config{
			Name:      runName,
			Agent:     "e2e-test",
			Workspace: config.WorkspaceConfig{Mode: "volume"},
			Mounts: []config.MountEntry{
				{Source: ".", Target: "/workspace"},
			},
		},
		Cmd: []string{"sh", "-c", "echo CHANGE > /workspace/agent.txt && echo WROTE_OK"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	volName := run.WorkspaceVolumeName(r.ID)
	t.Cleanup(func() {
		runMoatCLI(t, "destroy", "--force", r.ID)
		_ = exec.Command("docker", "volume", "rm", volName).Run()
	})

	if err := mgr.Start(ctx, r.ID, run.StartOptions{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := mgr.Wait(ctx, r.ID); err != nil {
		t.Logf("Wait returned error: %v", err)
	}

	// Destroy without --force and without a snapshot must be refused.
	out, code := runMoatCLI(t, "destroy", r.ID)
	if code == 0 {
		t.Errorf("destroy without snapshot/force should fail, but exited 0\nOutput: %s", out)
	}
	if !strings.Contains(out, "no extraction snapshot") {
		t.Errorf("destroy guard error should mention missing extraction snapshot\nOutput: %s", out)
	}
	if !dockerVolumeExists(volName) {
		t.Errorf("workspace volume %s should still exist after refused destroy", volName)
	}

	// Forced destroy removes the run and its volume.
	if out, code := runMoatCLI(t, "destroy", "--force", r.ID); code != 0 {
		t.Fatalf("moat destroy --force exited %d\nOutput: %s", code, out)
	}
	if dockerVolumeExists(volName) {
		t.Errorf("workspace volume %s still exists after forced destroy", volName)
	}
}
