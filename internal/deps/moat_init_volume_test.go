// internal/deps/moat_init_volume_test.go
package deps

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestVolumeCopyInPipeline exercises the tar pipeline that the moat-init
// entrypoint's populate-workspace-volume phase runs (internal/moatinit
// shells to tar for the byte copy). It does NOT pass --no-dereference: GNU
// tar 1.34 (in the container image) does not recognize the long flag name,
// and symlink-preservation is already tar's default for `-cf`.
//
// The pipeline under test:
//
//	cd "$staging" && tar --exclude-from="$excludeFile" -cf - . \
//	  | ( cd "$workspace" && tar -xf - )
//
// The exclude file uses the REAL emitted format: newline-delimited,
// "./"-prefixed patterns (see run.workspaceExcludes). NUL delimiting is NOT
// used — GNU tar 1.34's --null --exclude-from only applies the first record,
// silently dropping the rest.
//
// It asserts four invariants:
//  1. A single-component excluded path (node_modules) is absent.
//  2. A NESTED excluded path (dist/sub) is absent — this is the case that the
//     old "single-component only / NUL-delimited" pipeline silently copied in.
//  3. Non-excluded files are present (including a sibling of the excluded
//     nested dir, dist/keep, proving the exclude is scoped).
//  4. Symlinks are preserved as symlinks (not dereferenced) — GNU tar default.
func TestVolumeCopyInPipeline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX tar pipeline; not run on Windows")
	}

	staging := t.TempDir()
	workspace := t.TempDir()

	// Single-component directory that should be excluded.
	mkdir(t, filepath.Join(staging, "node_modules"))
	write(t, filepath.Join(staging, "node_modules", "big"), "x")

	// Nested directory that should be excluded (dist/sub), plus a sibling
	// (dist/keep) that must survive — proves the nested exclude is scoped.
	mkdir(t, filepath.Join(staging, "dist", "sub"))
	write(t, filepath.Join(staging, "dist", "sub", "nested"), "x")
	mkdir(t, filepath.Join(staging, "dist", "keep"))
	write(t, filepath.Join(staging, "dist", "keep", "file"), "x")

	// Regular file that should be copied.
	write(t, filepath.Join(staging, "main.go"), "package main")

	// Dangling symlink — must be preserved as a symlink, not dereferenced.
	if err := os.Symlink("/etc/hostname", filepath.Join(staging, "danglink")); err != nil {
		t.Fatal(err)
	}

	// Real emitted format: newline-delimited, "./"-prefixed patterns.
	excludeFile := filepath.Join(t.TempDir(), "excludes")
	write(t, excludeFile, "./node_modules\n./dist/sub\n")

	// Pipeline without --no-dereference, matching the script: symlink-preservation
	// is tar's default for `-cf`, and GNU tar 1.34 doesn't recognize the long flag
	// name. Invariant 4 below confirms symlinks survive without it.
	cmd := exec.Command("sh", "-c",
		`set -e; cd "$1"; tar --exclude-from="$3" -cf - . | (cd "$2" && tar -xf -)`,
		"sh", staging, workspace, excludeFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("copy-in pipeline failed: %v\n%s", err, out)
	}

	// 1. Single-component exclude absent.
	if _, err := os.Stat(filepath.Join(workspace, "node_modules")); !os.IsNotExist(err) {
		t.Errorf("node_modules should be excluded, stat err=%v", err)
	}

	// 2. Nested exclude absent (the previously-broken case).
	if _, err := os.Stat(filepath.Join(workspace, "dist", "sub")); !os.IsNotExist(err) {
		t.Errorf("dist/sub should be excluded, stat err=%v", err)
	}

	// 3a. Non-excluded sibling of the nested exclude present.
	if _, err := os.Stat(filepath.Join(workspace, "dist", "keep", "file")); err != nil {
		t.Errorf("dist/keep/file should be copied: %v", err)
	}

	// 3b. Top-level non-excluded file present.
	if _, err := os.Stat(filepath.Join(workspace, "main.go")); err != nil {
		t.Errorf("main.go should be copied: %v", err)
	}

	// 4. Symlink preserved as a symlink (not dereferenced).
	fi, err := os.Lstat(filepath.Join(workspace, "danglink"))
	if err != nil {
		t.Fatalf("danglink should exist in workspace: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("danglink should be a symlink, got mode=%v", fi.Mode())
	}
}

// TestVolumeCopyInPipelineEmptyExcludes verifies that an empty exclude file
// (MOAT_WORKSPACE_EXCLUDES unset → empty file) still copies everything.
func TestVolumeCopyInPipelineEmptyExcludes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX tar pipeline; not run on Windows")
	}

	staging := t.TempDir()
	workspace := t.TempDir()
	write(t, filepath.Join(staging, "main.go"), "package main")
	write(t, filepath.Join(staging, "README"), "hello")

	// Empty exclude file (equivalent to MOAT_WORKSPACE_EXCLUDES unset).
	excludeFile := filepath.Join(t.TempDir(), "excludes")
	write(t, excludeFile, "")

	cmd := exec.Command("sh", "-c",
		`set -e; cd "$1"; tar --exclude-from="$3" -cf - . | (cd "$2" && tar -xf -)`,
		"sh", staging, workspace, excludeFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("copy-in pipeline failed: %v\n%s", err, out)
	}

	for _, f := range []string{"main.go", "README"} {
		if _, err := os.Stat(filepath.Join(workspace, f)); err != nil {
			t.Errorf("%s should be copied with empty excludes: %v", f, err)
		}
	}
}

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}
