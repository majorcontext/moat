//go:build darwin

package snapshot

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAPFSBackendName(t *testing.T) {
	backend := NewAPFSBackend(t.TempDir())
	if got := backend.Name(); got != "apfs" {
		t.Errorf("Name() = %q, want %q", got, "apfs")
	}
}

func TestAPFSBackendImplementsInterface(t *testing.T) {
	// Compile-time check is in the main file, but this verifies at runtime
	var _ Backend = (*APFSBackend)(nil)
}

func TestAPFSBackendCreateAndRestore(t *testing.T) {
	if !IsAPFS(os.TempDir()) {
		t.Skip("temp directory is not on APFS, skipping test")
	}

	// Create a workspace with some files
	workspace := t.TempDir()
	snapshotDir := t.TempDir()

	// Create test files
	if err := os.WriteFile(filepath.Join(workspace, "file1.txt"), []byte("content1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "subdir", "file2.txt"), []byte("content2"), 0644); err != nil {
		t.Fatal(err)
	}

	backend := NewAPFSBackend(snapshotDir)

	// Create a snapshot
	clonePath, err := backend.Create(workspace, "snap_test_123")
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	// Verify clone exists
	if _, err := os.Stat(clonePath); os.IsNotExist(err) {
		t.Fatalf("clone path does not exist: %s", clonePath)
	}

	// Verify clone contents
	content, err := os.ReadFile(filepath.Join(clonePath, "file1.txt"))
	if err != nil {
		t.Fatalf("failed to read file1.txt from clone: %v", err)
	}
	if string(content) != "content1" {
		t.Errorf("file1.txt content = %q, want %q", content, "content1")
	}

	content, err = os.ReadFile(filepath.Join(clonePath, "subdir", "file2.txt"))
	if err != nil {
		t.Fatalf("failed to read subdir/file2.txt from clone: %v", err)
	}
	if string(content) != "content2" {
		t.Errorf("subdir/file2.txt content = %q, want %q", content, "content2")
	}

	// Modify the workspace
	if err := os.WriteFile(filepath.Join(workspace, "file1.txt"), []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}

	// Restore the snapshot
	if err := backend.Restore(workspace, clonePath); err != nil {
		t.Fatalf("Restore() failed: %v", err)
	}

	// Verify restored content
	content, err = os.ReadFile(filepath.Join(workspace, "file1.txt"))
	if err != nil {
		t.Fatalf("failed to read restored file1.txt: %v", err)
	}
	if string(content) != "content1" {
		t.Errorf("restored file1.txt content = %q, want %q", content, "content1")
	}

	// Clean up
	if err := backend.Delete(clonePath); err != nil {
		t.Fatalf("Delete() failed: %v", err)
	}

	// Verify clone is deleted
	if _, err := os.Stat(clonePath); !os.IsNotExist(err) {
		t.Errorf("clone should be deleted, but stat returned: %v", err)
	}
}

func TestAPFSBackendRemovesGitDir(t *testing.T) {
	if !IsAPFS(os.TempDir()) {
		t.Skip("temp directory is not on APFS, skipping test")
	}

	workspace := t.TempDir()
	snapshotDir := t.TempDir()

	// Create a .git directory
	gitDir := filepath.Join(workspace, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("git config"), 0644); err != nil {
		t.Fatal(err)
	}

	backend := NewAPFSBackend(snapshotDir)
	clonePath, err := backend.Create(workspace, "snap_git_test")
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}
	defer func() { _ = backend.Delete(clonePath) }()

	// Verify .git is NOT in the clone (should be removed to save space)
	cloneGitDir := filepath.Join(clonePath, ".git")
	if _, err := os.Stat(cloneGitDir); !os.IsNotExist(err) {
		t.Errorf(".git directory should not exist in clone, but stat returned: %v", err)
	}
}

func TestAPFSBackendList(t *testing.T) {
	if !IsAPFS(os.TempDir()) {
		t.Skip("temp directory is not on APFS, skipping test")
	}

	workspace := t.TempDir()
	snapshotDir := t.TempDir()

	backend := NewAPFSBackend(snapshotDir)

	// Create some snapshots
	clone1, err := backend.Create(workspace, "snap_list_1")
	if err != nil {
		t.Fatalf("Create() snap_list_1 failed: %v", err)
	}
	defer func() { _ = backend.Delete(clone1) }()

	clone2, err := backend.Create(workspace, "snap_list_2")
	if err != nil {
		t.Fatalf("Create() snap_list_2 failed: %v", err)
	}
	defer func() { _ = backend.Delete(clone2) }()

	// List snapshots
	clones, err := backend.List(workspace)
	if err != nil {
		t.Fatalf("List() failed: %v", err)
	}

	if len(clones) != 2 {
		t.Errorf("List() returned %d clones, want 2", len(clones))
	}
}

func TestAPFSBackendRestoreTo(t *testing.T) {
	if !IsAPFS(os.TempDir()) {
		t.Skip("temp directory is not on APFS, skipping test")
	}

	workspace := t.TempDir()
	snapshotDir := t.TempDir()
	destDir := t.TempDir()

	// Create a test file
	if err := os.WriteFile(filepath.Join(workspace, "test.txt"), []byte("test content"), 0644); err != nil {
		t.Fatal(err)
	}

	backend := NewAPFSBackend(snapshotDir)

	clonePath, err := backend.Create(workspace, "snap_restore_to")
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}
	defer func() { _ = backend.Delete(clonePath) }()

	// Restore to different directory
	if err := backend.RestoreTo(clonePath, destDir); err != nil {
		t.Fatalf("RestoreTo() failed: %v", err)
	}

	// Verify content was restored
	content, err := os.ReadFile(filepath.Join(destDir, "test.txt"))
	if err != nil {
		t.Fatalf("failed to read test.txt from destination: %v", err)
	}
	if string(content) != "test content" {
		t.Errorf("test.txt content = %q, want %q", content, "test content")
	}
}

func TestGetMountPoint(t *testing.T) {
	// Test with root path which should always work
	mountPoint, err := getMountPoint("/")
	if err != nil {
		t.Fatalf("getMountPoint(\"/\") failed: %v", err)
	}

	if mountPoint != "/" {
		t.Errorf("getMountPoint(\"/\") = %q, want \"/\"", mountPoint)
	}
}

func TestIsAPFSOnRoot(t *testing.T) {
	// Check if diskutil is available
	if _, err := exec.LookPath("diskutil"); err != nil {
		t.Skip("diskutil not available, skipping APFS check test")
	}

	// Modern macOS systems use APFS for the root volume
	// This test verifies the function works without panicking
	result := IsAPFS("/")
	t.Logf("IsAPFS(\"/\") = %v", result)

	// On modern macOS (10.13+), root should be APFS
	// But we don't strictly assert this since older systems might differ
}
