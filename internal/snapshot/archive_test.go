package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveBackend(t *testing.T) {
	// Create temp directories for workspace and snapshots
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Create test files in workspace
	if err := os.WriteFile(filepath.Join(workspaceDir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to create main.go: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceDir, "pkg"), 0755); err != nil {
		t.Fatalf("failed to create pkg dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "pkg/lib.go"), []byte("package pkg\n"), 0644); err != nil {
		t.Fatalf("failed to create pkg/lib.go: %v", err)
	}

	// Create backend
	backend := NewArchiveBackend(snapshotDir, ArchiveOptions{})

	// Verify Name()
	if backend.Name() != "archive" {
		t.Errorf("Name() = %q, want %q", backend.Name(), "archive")
	}

	// Create snapshot
	snapID := "snap_test001"
	nativeRef, err := backend.Create(workspaceDir, snapID)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Verify archive file exists
	if _, err := os.Stat(nativeRef); os.IsNotExist(err) {
		t.Errorf("archive file does not exist: %s", nativeRef)
	}

	// Modify workspace
	if err := os.WriteFile(filepath.Join(workspaceDir, "main.go"), []byte("package main // modified\n"), 0644); err != nil {
		t.Fatalf("failed to modify main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "newfile.txt"), []byte("new content\n"), 0644); err != nil {
		t.Fatalf("failed to create newfile.txt: %v", err)
	}

	// Restore snapshot
	if err := backend.Restore(workspaceDir, nativeRef); err != nil {
		t.Fatalf("Restore() error: %v", err)
	}

	// Verify main.go was restored to original content
	content, err := os.ReadFile(filepath.Join(workspaceDir, "main.go"))
	if err != nil {
		t.Fatalf("failed to read main.go: %v", err)
	}
	if string(content) != "package main\n" {
		t.Errorf("main.go content = %q, want %q", string(content), "package main\n")
	}

	// Verify newfile.txt was removed (clean restore)
	if _, err := os.Stat(filepath.Join(workspaceDir, "newfile.txt")); !os.IsNotExist(err) {
		t.Errorf("newfile.txt should not exist after restore")
	}

	// Verify pkg/lib.go still exists
	content, err = os.ReadFile(filepath.Join(workspaceDir, "pkg/lib.go"))
	if err != nil {
		t.Fatalf("failed to read pkg/lib.go: %v", err)
	}
	if string(content) != "package pkg\n" {
		t.Errorf("pkg/lib.go content = %q, want %q", string(content), "package pkg\n")
	}

	// Test List
	refs, err := backend.List(workspaceDir)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(refs) != 1 {
		t.Errorf("List() returned %d refs, want 1", len(refs))
	}

	// Delete snapshot
	if err := backend.Delete(nativeRef); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	// Verify archive file was deleted
	if _, err := os.Stat(nativeRef); !os.IsNotExist(err) {
		t.Errorf("archive file should not exist after Delete: %s", nativeRef)
	}

	// Verify List returns empty after delete
	refs, err = backend.List(workspaceDir)
	if err != nil {
		t.Fatalf("List() error after delete: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("List() returned %d refs after delete, want 0", len(refs))
	}
}

func TestArchiveBackendRestoreTo(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()
	restoreDir := t.TempDir()

	// Create test files in workspace
	if err := os.WriteFile(filepath.Join(workspaceDir, "app.go"), []byte("package app\n"), 0644); err != nil {
		t.Fatalf("failed to create app.go: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceDir, "internal"), 0755); err != nil {
		t.Fatalf("failed to create internal dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "internal/core.go"), []byte("package internal\n"), 0644); err != nil {
		t.Fatalf("failed to create internal/core.go: %v", err)
	}

	// Create backend and snapshot
	backend := NewArchiveBackend(snapshotDir, ArchiveOptions{})
	snapID := "snap_restore"
	nativeRef, err := backend.Create(workspaceDir, snapID)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// RestoreTo different directory
	if err := backend.RestoreTo(nativeRef, restoreDir); err != nil {
		t.Fatalf("RestoreTo() error: %v", err)
	}

	// Verify files exist in restore directory
	content, err := os.ReadFile(filepath.Join(restoreDir, "app.go"))
	if err != nil {
		t.Fatalf("failed to read app.go in restore dir: %v", err)
	}
	if string(content) != "package app\n" {
		t.Errorf("app.go content = %q, want %q", string(content), "package app\n")
	}

	content, err = os.ReadFile(filepath.Join(restoreDir, "internal/core.go"))
	if err != nil {
		t.Fatalf("failed to read internal/core.go in restore dir: %v", err)
	}
	if string(content) != "package internal\n" {
		t.Errorf("internal/core.go content = %q, want %q", string(content), "package internal\n")
	}

	// Original workspace should be untouched
	content, err = os.ReadFile(filepath.Join(workspaceDir, "app.go"))
	if err != nil {
		t.Fatalf("original workspace app.go missing: %v", err)
	}
	if string(content) != "package app\n" {
		t.Errorf("original app.go modified unexpectedly")
	}
}

func TestArchiveBackendGitignore(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Create .gitignore
	gitignore := `node_modules/
*.log
build/
.env
`
	if err := os.WriteFile(filepath.Join(workspaceDir, ".gitignore"), []byte(gitignore), 0644); err != nil {
		t.Fatalf("failed to create .gitignore: %v", err)
	}

	// Create files that should be included
	if err := os.WriteFile(filepath.Join(workspaceDir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to create main.go: %v", err)
	}

	// Create files/directories that should be excluded
	if err := os.MkdirAll(filepath.Join(workspaceDir, "node_modules/pkg"), 0755); err != nil {
		t.Fatalf("failed to create node_modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "node_modules/pkg/index.js"), []byte("module.exports = {}\n"), 0644); err != nil {
		t.Fatalf("failed to create node_modules file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "app.log"), []byte("log entry\n"), 0644); err != nil {
		t.Fatalf("failed to create app.log: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceDir, "build"), 0755); err != nil {
		t.Fatalf("failed to create build dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "build/output.js"), []byte("compiled\n"), 0644); err != nil {
		t.Fatalf("failed to create build/output.js: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, ".env"), []byte("SECRET=value\n"), 0644); err != nil {
		t.Fatalf("failed to create .env: %v", err)
	}

	// Create backend with gitignore support
	backend := NewArchiveBackend(snapshotDir, ArchiveOptions{UseGitignore: true})
	snapID := "snap_gitignore"
	nativeRef, err := backend.Create(workspaceDir, snapID)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Restore to a clean directory to verify what was archived
	restoreDir := t.TempDir()
	if err := backend.RestoreTo(nativeRef, restoreDir); err != nil {
		t.Fatalf("RestoreTo() error: %v", err)
	}

	// Verify included files exist
	if _, err := os.Stat(filepath.Join(restoreDir, "main.go")); os.IsNotExist(err) {
		t.Errorf("main.go should exist in restored snapshot")
	}
	if _, err := os.Stat(filepath.Join(restoreDir, ".gitignore")); os.IsNotExist(err) {
		t.Errorf(".gitignore should exist in restored snapshot")
	}

	// Verify excluded files/directories do not exist
	if _, err := os.Stat(filepath.Join(restoreDir, "node_modules")); !os.IsNotExist(err) {
		t.Errorf("node_modules should NOT exist in restored snapshot")
	}
	if _, err := os.Stat(filepath.Join(restoreDir, "app.log")); !os.IsNotExist(err) {
		t.Errorf("app.log should NOT exist in restored snapshot")
	}
	if _, err := os.Stat(filepath.Join(restoreDir, "build")); !os.IsNotExist(err) {
		t.Errorf("build should NOT exist in restored snapshot")
	}
	if _, err := os.Stat(filepath.Join(restoreDir, ".env")); !os.IsNotExist(err) {
		t.Errorf(".env should NOT exist in restored snapshot")
	}
}

func TestArchiveBackendAdditionalExcludes(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Create test files
	if err := os.WriteFile(filepath.Join(workspaceDir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to create main.go: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceDir, "vendor"), 0755); err != nil {
		t.Fatalf("failed to create vendor dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "vendor/dep.go"), []byte("package vendor\n"), 0644); err != nil {
		t.Fatalf("failed to create vendor/dep.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "debug.tmp"), []byte("temp data\n"), 0644); err != nil {
		t.Fatalf("failed to create debug.tmp: %v", err)
	}

	// Create backend with additional excludes
	backend := NewArchiveBackend(snapshotDir, ArchiveOptions{
		Additional: []string{"vendor/", "*.tmp"},
	})
	snapID := "snap_additional"
	nativeRef, err := backend.Create(workspaceDir, snapID)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Restore to verify
	restoreDir := t.TempDir()
	if err := backend.RestoreTo(nativeRef, restoreDir); err != nil {
		t.Fatalf("RestoreTo() error: %v", err)
	}

	// Verify included files exist
	if _, err := os.Stat(filepath.Join(restoreDir, "main.go")); os.IsNotExist(err) {
		t.Errorf("main.go should exist")
	}

	// Verify excluded files/directories do not exist
	if _, err := os.Stat(filepath.Join(restoreDir, "vendor")); !os.IsNotExist(err) {
		t.Errorf("vendor should NOT exist")
	}
	if _, err := os.Stat(filepath.Join(restoreDir, "debug.tmp")); !os.IsNotExist(err) {
		t.Errorf("debug.tmp should NOT exist")
	}
}

func TestArchiveBackendPreservesGitDirOnRestore(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Create test files
	if err := os.WriteFile(filepath.Join(workspaceDir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to create main.go: %v", err)
	}

	// Create a fake .git directory (should NOT be archived, but should be preserved on restore)
	if err := os.MkdirAll(filepath.Join(workspaceDir, ".git/objects"), 0755); err != nil {
		t.Fatalf("failed to create .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, ".git/HEAD"), []byte("ref: refs/heads/main\n"), 0644); err != nil {
		t.Fatalf("failed to create .git/HEAD: %v", err)
	}

	// Create backend (should exclude .git by default)
	backend := NewArchiveBackend(snapshotDir, ArchiveOptions{})
	snapID := "snap_git"
	nativeRef, err := backend.Create(workspaceDir, snapID)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Modify workspace files
	if err := os.WriteFile(filepath.Join(workspaceDir, "main.go"), []byte("package main // changed\n"), 0644); err != nil {
		t.Fatalf("failed to modify main.go: %v", err)
	}
	// Also add a marker to .git to verify it's preserved
	if err := os.WriteFile(filepath.Join(workspaceDir, ".git/marker"), []byte("keep me\n"), 0644); err != nil {
		t.Fatalf("failed to create .git/marker: %v", err)
	}

	// Restore snapshot
	if err := backend.Restore(workspaceDir, nativeRef); err != nil {
		t.Fatalf("Restore() error: %v", err)
	}

	// Verify main.go was restored
	content, err := os.ReadFile(filepath.Join(workspaceDir, "main.go"))
	if err != nil {
		t.Fatalf("failed to read main.go: %v", err)
	}
	if string(content) != "package main\n" {
		t.Errorf("main.go content = %q, want %q", string(content), "package main\n")
	}

	// Verify .git directory was preserved (including the marker file we added after snapshot)
	if _, err := os.Stat(filepath.Join(workspaceDir, ".git/HEAD")); os.IsNotExist(err) {
		t.Errorf(".git/HEAD should exist after restore")
	}
	content, err = os.ReadFile(filepath.Join(workspaceDir, ".git/marker"))
	if err != nil {
		t.Fatalf(".git/marker should exist after restore: %v", err)
	}
	if string(content) != "keep me\n" {
		t.Errorf(".git/marker content = %q, want %q", string(content), "keep me\n")
	}
}

func TestArchiveBackendSymlinks(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Create a file and a symlink to it
	if err := os.WriteFile(filepath.Join(workspaceDir, "target.txt"), []byte("target content\n"), 0644); err != nil {
		t.Fatalf("failed to create target.txt: %v", err)
	}
	if err := os.Symlink("target.txt", filepath.Join(workspaceDir, "link.txt")); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	// Create backend and snapshot
	backend := NewArchiveBackend(snapshotDir, ArchiveOptions{})
	snapID := "snap_symlink"
	nativeRef, err := backend.Create(workspaceDir, snapID)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Restore to a clean directory
	restoreDir := t.TempDir()
	if err := backend.RestoreTo(nativeRef, restoreDir); err != nil {
		t.Fatalf("RestoreTo() error: %v", err)
	}

	// Verify target file exists
	content, err := os.ReadFile(filepath.Join(restoreDir, "target.txt"))
	if err != nil {
		t.Fatalf("failed to read target.txt: %v", err)
	}
	if string(content) != "target content\n" {
		t.Errorf("target.txt content = %q, want %q", string(content), "target content\n")
	}

	// Verify symlink exists and points to the right target
	link, err := os.Readlink(filepath.Join(restoreDir, "link.txt"))
	if err != nil {
		t.Fatalf("failed to read symlink: %v", err)
	}
	if link != "target.txt" {
		t.Errorf("symlink target = %q, want %q", link, "target.txt")
	}

	// Verify we can read through the symlink
	content, err = os.ReadFile(filepath.Join(restoreDir, "link.txt"))
	if err != nil {
		t.Fatalf("failed to read through symlink: %v", err)
	}
	if string(content) != "target content\n" {
		t.Errorf("symlink content = %q, want %q", string(content), "target content\n")
	}
}

func TestArchiveBackendListMultiple(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Create a test file
	if err := os.WriteFile(filepath.Join(workspaceDir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to create main.go: %v", err)
	}

	// Create backend
	backend := NewArchiveBackend(snapshotDir, ArchiveOptions{})

	// Create multiple snapshots
	refs := make([]string, 3)
	for i := 0; i < 3; i++ {
		snapID := NewID() // Use real ID generation
		ref, err := backend.Create(workspaceDir, snapID)
		if err != nil {
			t.Fatalf("Create() error: %v", err)
		}
		refs[i] = ref
	}

	// List snapshots
	listed, err := backend.List(workspaceDir)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(listed) != 3 {
		t.Errorf("List() returned %d refs, want 3", len(listed))
	}

	// Verify all refs are in the list
	for _, ref := range refs {
		found := false
		for _, l := range listed {
			if l == ref {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ref %s not found in List() results", ref)
		}
	}
}

func TestArchiveBackendFilePermissions(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Create files with different permissions
	if err := os.WriteFile(filepath.Join(workspaceDir, "script.sh"), []byte("#!/bin/bash\necho hello\n"), 0755); err != nil {
		t.Fatalf("failed to create script.sh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "data.txt"), []byte("data\n"), 0644); err != nil {
		t.Fatalf("failed to create data.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "readonly.txt"), []byte("read only\n"), 0444); err != nil {
		t.Fatalf("failed to create readonly.txt: %v", err)
	}

	// Create backend and snapshot
	backend := NewArchiveBackend(snapshotDir, ArchiveOptions{})
	snapID := "snap_perms"
	nativeRef, err := backend.Create(workspaceDir, snapID)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Restore to a clean directory
	restoreDir := t.TempDir()
	if err := backend.RestoreTo(nativeRef, restoreDir); err != nil {
		t.Fatalf("RestoreTo() error: %v", err)
	}

	// Verify permissions (mask out umask differences by checking executable bit)
	info, err := os.Stat(filepath.Join(restoreDir, "script.sh"))
	if err != nil {
		t.Fatalf("failed to stat script.sh: %v", err)
	}
	if info.Mode().Perm()&0111 == 0 {
		t.Errorf("script.sh should be executable, got mode %o", info.Mode().Perm())
	}

	info, err = os.Stat(filepath.Join(restoreDir, "data.txt"))
	if err != nil {
		t.Fatalf("failed to stat data.txt: %v", err)
	}
	if info.Mode().Perm()&0111 != 0 {
		t.Errorf("data.txt should not be executable, got mode %o", info.Mode().Perm())
	}
}

func TestArchiveBackendNestedGitignore(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Create root .gitignore
	if err := os.WriteFile(filepath.Join(workspaceDir, ".gitignore"), []byte("*.log\n"), 0644); err != nil {
		t.Fatalf("failed to create .gitignore: %v", err)
	}

	// Create subdirectory with its own .gitignore
	if err := os.MkdirAll(filepath.Join(workspaceDir, "subdir"), 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "subdir/.gitignore"), []byte("*.tmp\n"), 0644); err != nil {
		t.Fatalf("failed to create subdir/.gitignore: %v", err)
	}

	// Create files
	if err := os.WriteFile(filepath.Join(workspaceDir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to create main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "app.log"), []byte("log\n"), 0644); err != nil {
		t.Fatalf("failed to create app.log: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "subdir/code.go"), []byte("package subdir\n"), 0644); err != nil {
		t.Fatalf("failed to create subdir/code.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "subdir/cache.tmp"), []byte("temp\n"), 0644); err != nil {
		t.Fatalf("failed to create subdir/cache.tmp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "subdir/debug.log"), []byte("debug\n"), 0644); err != nil {
		t.Fatalf("failed to create subdir/debug.log: %v", err)
	}

	// Create backend with gitignore support
	backend := NewArchiveBackend(snapshotDir, ArchiveOptions{UseGitignore: true})
	snapID := "snap_nested"
	nativeRef, err := backend.Create(workspaceDir, snapID)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Restore to verify
	restoreDir := t.TempDir()
	if err := backend.RestoreTo(nativeRef, restoreDir); err != nil {
		t.Fatalf("RestoreTo() error: %v", err)
	}

	// Verify included files
	if _, err := os.Stat(filepath.Join(restoreDir, "main.go")); os.IsNotExist(err) {
		t.Errorf("main.go should exist")
	}
	if _, err := os.Stat(filepath.Join(restoreDir, "subdir/code.go")); os.IsNotExist(err) {
		t.Errorf("subdir/code.go should exist")
	}

	// Verify excluded files
	if _, err := os.Stat(filepath.Join(restoreDir, "app.log")); !os.IsNotExist(err) {
		t.Errorf("app.log should NOT exist (excluded by root .gitignore)")
	}
	if _, err := os.Stat(filepath.Join(restoreDir, "subdir/cache.tmp")); !os.IsNotExist(err) {
		t.Errorf("subdir/cache.tmp should NOT exist (excluded by subdir/.gitignore)")
	}
	if _, err := os.Stat(filepath.Join(restoreDir, "subdir/debug.log")); !os.IsNotExist(err) {
		t.Errorf("subdir/debug.log should NOT exist (excluded by root .gitignore)")
	}
}

func TestArchiveBackendEmptyWorkspace(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Create backend and snapshot of empty workspace
	backend := NewArchiveBackend(snapshotDir, ArchiveOptions{})
	snapID := "snap_empty"
	nativeRef, err := backend.Create(workspaceDir, snapID)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Verify archive was created
	if !strings.HasSuffix(nativeRef, ".tar.gz") {
		t.Errorf("nativeRef should end with .tar.gz, got %s", nativeRef)
	}

	// Restore to verify (should succeed even if empty)
	restoreDir := t.TempDir()
	if err := backend.RestoreTo(nativeRef, restoreDir); err != nil {
		t.Fatalf("RestoreTo() error: %v", err)
	}
}
