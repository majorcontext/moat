package moatinit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupStagingTree builds the staging fixture mirrored from the existing
// deps.TestVolumeCopyInPipeline: excluded single-component and nested dirs,
// a kept sibling, and a dangling symlink.
func setupStagingTree(t *testing.T, ts *testSys) {
	t.Helper()
	root := ts.Root
	for _, dir := range []string{"mnt/host-workspace/node_modules", "mnt/host-workspace/dist/sub", "mnt/host-workspace/dist/keep", "workspace"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"mnt/host-workspace/main.go":                "package main",
		"mnt/host-workspace/node_modules/pkg.json":  "{}",
		"mnt/host-workspace/dist/sub/bundle.js":     "x",
		"mnt/host-workspace/dist/keep/artifact.txt": "keep",
	}
	for p, content := range files {
		if err := os.WriteFile(filepath.Join(root, p), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/nonexistent-target", filepath.Join(root, "mnt/host-workspace/dangling")); err != nil {
		t.Fatal(err)
	}
}

// TestPopulateWorkspaceVolumeRealTar runs the phase with the real tar pipe
// against the injected root: excludes applied in full, symlinks preserved,
// every extracted node re-owned via lchown, temp exclude file cleaned up.
func TestPopulateWorkspaceVolumeRealTar(t *testing.T) {
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar not installed")
	}
	ts := newTestSys(t, 0, true)
	setupStagingTree(t, ts)

	ctx, _ := newTestContext(ts, Config{
		WorkspaceVolume:   "1",
		WorkspaceExcludes: "./node_modules\n./dist/sub",
		Home:              "/root",
	})
	if err := populateWorkspaceVolumePhase(ctx); err != nil {
		t.Fatal(err)
	}

	// Excludes: single-component and nested both absent (the GNU tar 1.34
	// newline --exclude-from contract), sibling of the nested exclude kept.
	if exists(ts, "/workspace/node_modules") {
		t.Error("excluded node_modules was copied")
	}
	if exists(ts, "/workspace/dist/sub") {
		t.Error("excluded dist/sub was copied")
	}
	if !exists(ts, "/workspace/dist/keep/artifact.txt") {
		t.Error("non-excluded dist/keep missing")
	}
	if got := fileContent(t, ts, "/workspace/main.go"); got != "package main" {
		t.Errorf("main.go = %q", got)
	}
	// WS-08: the dangling symlink is copied as a symlink, never dereferenced.
	info, err := os.Lstat(filepath.Join(ts.Root, "workspace/dangling"))
	if err != nil {
		t.Fatal("dangling symlink missing from /workspace")
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink was dereferenced during the copy")
	}

	// WS-10: every node re-owned — via lchown, so the symlink itself (not
	// its target) is in the set.
	for _, p := range []string{"/workspace", "/workspace/main.go", "/workspace/dist", "/workspace/dangling"} {
		if !ts.chowned(p) {
			t.Errorf("missing chown for %s", p)
		}
	}
	for _, c := range ts.chowns {
		if !c.lchown {
			t.Errorf("chown of %s did not use lchown", c.path)
		}
	}

	// WS-11: temp exclude file removed.
	matches, _ := filepath.Glob(filepath.Join(ts.Root, "tmp/moat-excludes.*"))
	if len(matches) != 0 {
		t.Errorf("exclude temp files left behind: %v", matches)
	}
}

// TestPopulateWorkspaceVolumeEmptyExcludes is the WS-06 companion to the
// exclude-applying test: an empty exclude file excludes nothing, so the
// whole staging tree lands in /workspace.
func TestPopulateWorkspaceVolumeEmptyExcludes(t *testing.T) {
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar not installed")
	}
	ts := newTestSys(t, 0, true)
	setupStagingTree(t, ts)
	ctx, _ := newTestContext(ts, Config{WorkspaceVolume: "1", Home: "/root"})
	if err := populateWorkspaceVolumePhase(ctx); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"/workspace/main.go",
		"/workspace/node_modules/pkg.json",
		"/workspace/dist/sub/bundle.js",
		"/workspace/dist/keep/artifact.txt",
	} {
		if !exists(ts, p) {
			t.Errorf("%s missing with empty excludes", p)
		}
	}
}

func TestPopulateWorkspaceVolumeGate(t *testing.T) {
	// WS-01: any non-"1" value is a no-op — checked BEFORE the root guard,
	// so a disabled populate as non-root is fine.
	for _, v := range []string{"", "0", "true", " 1", "01"} {
		ts := newTestSys(t, 1000, true)
		ctx, _ := newTestContext(ts, Config{WorkspaceVolume: v, Home: "/root"})
		if err := populateWorkspaceVolumePhase(ctx); err != nil {
			t.Errorf("WorkspaceVolume=%q: %v", v, err)
		}
		if len(ts.pipes) != 0 {
			t.Errorf("WorkspaceVolume=%q ran tar", v)
		}
	}
}

func TestPopulateWorkspaceVolumeRootGuard(t *testing.T) {
	// WS-02: enabled as non-root is fatal with the exact message.
	ts := newTestSys(t, 1000, true)
	ctx, stderr := newTestContext(ts, Config{WorkspaceVolume: "1", Home: "/root"})
	err := populateWorkspaceVolumePhase(ctx)
	exit, ok := err.(exitError)
	if !ok || exit.code != 1 {
		t.Fatalf("err = %v, want exitError{1}", err)
	}
	if stderr.String() != "moat: populate_workspace_volume must run as root\n" {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestPopulateWorkspaceVolumeMissingStagingFatal(t *testing.T) {
	// B-P2: a missing staging root is fatal (src leg cannot start), never a
	// silently empty /workspace.
	ts := newTestSys(t, 0, true)
	if err := os.MkdirAll(filepath.Join(ts.Root, "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, stderr := newTestContext(ts, Config{WorkspaceVolume: "1", Home: "/root"})
	err := populateWorkspaceVolumePhase(ctx)
	exit, ok := err.(exitError)
	if !ok || exit.code != 1 {
		t.Fatalf("err = %v, want exitError{1}", err)
	}
	if !strings.Contains(stderr.String(), "moat: failed to populate workspace volume (src=1 dst=0)") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestPopulateWorkspaceVolumeRCClassification(t *testing.T) {
	// WS-09: either end failing is fatal, with both codes reported.
	cases := []struct {
		src, dst int
		want     string
	}{
		{2, 0, "moat: failed to populate workspace volume (src=2 dst=0)"},
		{0, 2, "moat: failed to populate workspace volume (src=0 dst=2)"},
	}
	for _, tc := range cases {
		ts := newTestSys(t, 0, true)
		setupStagingTree(t, ts)
		ts.pipeHook = func(src, dst Cmd) (int, int, error) { return tc.src, tc.dst, nil }
		ctx, stderr := newTestContext(ts, Config{WorkspaceVolume: "1", Home: "/root"})
		err := populateWorkspaceVolumePhase(ctx)
		if exit, ok := err.(exitError); !ok || exit.code != 1 {
			t.Fatalf("src=%d dst=%d: err = %v, want exitError{1}", tc.src, tc.dst, err)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("stderr = %q, want %q", stderr.String(), tc.want)
		}
		// No chown after a failed copy.
		if len(ts.chowns) != 0 {
			t.Error("chown ran after failed copy")
		}
		// WS-11: temp file cleaned up on the failure path too.
		matches, _ := filepath.Glob(filepath.Join(ts.Root, "tmp/moat-excludes.*"))
		if len(matches) != 0 {
			t.Errorf("exclude temp files left after failure: %v", matches)
		}
	}
}

func TestPopulateWorkspaceVolumeArgAssembly(t *testing.T) {
	// The assembled tar argv and exclude-file contents are the contract Go
	// now owns (§6): capture them via the pipe hook.
	ts := newTestSys(t, 0, true)
	setupStagingTree(t, ts)
	var excludeContent string
	ts.pipeHook = func(src, dst Cmd) (int, int, error) {
		for _, a := range src.Argv {
			if strings.HasPrefix(a, "--exclude-from=") {
				data, err := os.ReadFile(strings.TrimPrefix(a, "--exclude-from="))
				if err != nil {
					t.Fatalf("reading exclude file: %v", err)
				}
				excludeContent = string(data)
			}
		}
		return 0, 0, nil
	}
	ctx, _ := newTestContext(ts, Config{
		WorkspaceVolume:   "1",
		WorkspaceStaging:  "/custom/staging",
		WorkspaceExcludes: "./node_modules\n./dist/sub",
		Home:              "/root",
	})
	if err := os.MkdirAll(filepath.Join(ts.Root, "custom/staging"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := populateWorkspaceVolumePhase(ctx); err != nil {
		t.Fatal(err)
	}

	src, dst := ts.pipes[0][0], ts.pipes[0][1]
	if got := strings.Join(src.Argv[2:], " "); got != "-cf - ." {
		t.Errorf("src tar argv tail = %q, want '-cf - .'", got)
	}
	if src.Argv[0] != "tar" || !strings.HasPrefix(src.Argv[1], "--exclude-from=") {
		t.Errorf("src tar argv = %v", src.Argv)
	}
	if got := strings.Join(dst.Argv, " "); got != "tar -xf -" {
		t.Errorf("dst tar argv = %q, want 'tar -xf -'", got)
	}
	// WS-03: explicit staging honored (as the subprocess-visible path).
	if src.Dir != ts.RealPath("/custom/staging") {
		t.Errorf("src dir = %q", src.Dir)
	}
	if dst.Dir != ts.RealPath("/workspace") {
		t.Errorf("dst dir = %q", dst.Dir)
	}
	// WS-04: exclude file carries the env value byte-for-byte.
	if excludeContent != "./node_modules\n./dist/sub" {
		t.Errorf("exclude file = %q", excludeContent)
	}
}

func TestPopulateWorkspaceVolumeChownFailureFatal(t *testing.T) {
	// WS-10 companion: unlike the staging chowns, the /workspace re-own is
	// unguarded — a chown failure is fatal.
	ts := newTestSys(t, 0, true)
	setupStagingTree(t, ts)
	ts.pipeHook = func(src, dst Cmd) (int, int, error) { return 0, 0, nil }
	ts.chownErr = os.ErrPermission
	ctx, _ := newTestContext(ts, Config{WorkspaceVolume: "1", Home: "/root"})
	err := populateWorkspaceVolumePhase(ctx)
	if exit, ok := err.(exitError); !ok || exit.code != 1 {
		t.Fatalf("err = %v, want exitError{1}", err)
	}

	// And with no moatuser account the chown target is invalid: fatal too.
	ts2 := newTestSys(t, 0, false)
	setupStagingTree(t, ts2)
	ts2.pipeHook = func(src, dst Cmd) (int, int, error) { return 0, 0, nil }
	ctx2, _ := newTestContext(ts2, Config{WorkspaceVolume: "1", Home: "/root"})
	if exit, ok := populateWorkspaceVolumePhase(ctx2).(exitError); !ok || exit.code != 1 {
		t.Fatal("missing moatuser should make the re-own fatal")
	}
}

func TestNamedVolumeChownPhase(t *testing.T) {
	// Gate matrix (EXEC-07): runs only when all three hold.
	cases := []struct {
		name     string
		val      string
		euid     int
		moatuser bool
		want     int // chown calls
	}{
		{"all conditions", "/vol/a /vol/[b]", 0, true, 2},
		{"unset", "", 0, true, 0},
		{"non-root", "/vol/a", 1000, true, 0},
		{"no moatuser", "/vol/a", 0, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := newTestSys(t, tc.euid, tc.moatuser)
			ctx, _ := newTestContext(ts, Config{VolumeChown: tc.val, Home: "/root"})
			if err := namedVolumeChownPhase(ctx); err != nil {
				t.Fatal(err)
			}
			if len(ts.chowns) != tc.want {
				t.Errorf("chown calls = %d, want %d (%v)", len(ts.chowns), tc.want, ts.chowns)
			}
		})
	}

	// EXEC-08/09: glob chars stay literal, chown is non-recursive (exactly
	// the listed roots, nothing beneath), errors swallowed.
	ts := newTestSys(t, 0, true)
	ts.chownErr = os.ErrPermission
	ctx, _ := newTestContext(ts, Config{VolumeChown: "/r/[x] /r/normal", Home: "/root"})
	if err := namedVolumeChownPhase(ctx); err != nil {
		t.Fatalf("best-effort chown aborted: %v", err)
	}
	if !ts.chowned("/r/[x]") || !ts.chowned("/r/normal") {
		t.Errorf("chowns = %v", ts.chowns)
	}
	if len(ts.chowns) != 2 {
		t.Errorf("non-recursive chown touched %d paths", len(ts.chowns))
	}
}
