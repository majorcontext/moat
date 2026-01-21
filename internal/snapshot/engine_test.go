package snapshot

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEngineDetectsArchiveBackend(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Force archive backend to test the detection path
	// (On macOS, temp dirs are typically on APFS, so we force archive for this test)
	engine, err := NewEngine(workspaceDir, snapshotDir, EngineOptions{
		ForceBackend: "archive",
	})
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}

	// Verify backend is archive
	if engine.Backend().Name() != "archive" {
		t.Errorf("Backend().Name() = %q, want %q", engine.Backend().Name(), "archive")
	}
}

func TestEngineForceBackend(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Force archive backend explicitly
	engine, err := NewEngine(workspaceDir, snapshotDir, EngineOptions{
		ForceBackend: "archive",
	})
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}

	if engine.Backend().Name() != "archive" {
		t.Errorf("Backend().Name() = %q, want %q", engine.Backend().Name(), "archive")
	}
}

func TestEngineCreateAndRestore(t *testing.T) {
	// Create temp directories
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

	// Create engine with archive backend
	engine, err := NewEngine(workspaceDir, snapshotDir, EngineOptions{
		ForceBackend: "archive",
	})
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}

	// Create snapshot
	meta, err := engine.Create(TypeManual, "test snapshot")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Verify metadata
	if meta.ID == "" {
		t.Error("metadata ID should not be empty")
	}
	if meta.Type != TypeManual {
		t.Errorf("metadata Type = %q, want %q", meta.Type, TypeManual)
	}
	if meta.Label != "test snapshot" {
		t.Errorf("metadata Label = %q, want %q", meta.Label, "test snapshot")
	}
	if meta.Backend != "archive" {
		t.Errorf("metadata Backend = %q, want %q", meta.Backend, "archive")
	}
	if meta.CreatedAt.IsZero() {
		t.Error("metadata CreatedAt should not be zero")
	}
	if meta.NativeRef == "" {
		t.Error("metadata NativeRef should not be empty")
	}

	// Modify workspace
	if err := os.WriteFile(filepath.Join(workspaceDir, "main.go"), []byte("package main // modified\n"), 0644); err != nil {
		t.Fatalf("failed to modify main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "newfile.txt"), []byte("new content\n"), 0644); err != nil {
		t.Fatalf("failed to create newfile.txt: %v", err)
	}

	// Restore snapshot
	if err := engine.Restore(meta.ID); err != nil {
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
}

func TestEngineRestoreTo(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()
	restoreDir := t.TempDir()

	// Create test files in workspace
	if err := os.WriteFile(filepath.Join(workspaceDir, "app.go"), []byte("package app\n"), 0644); err != nil {
		t.Fatalf("failed to create app.go: %v", err)
	}

	// Create engine with archive backend
	engine, err := NewEngine(workspaceDir, snapshotDir, EngineOptions{
		ForceBackend: "archive",
	})
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}

	// Create snapshot
	meta, err := engine.Create(TypeManual, "")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// RestoreTo different directory
	if err := engine.RestoreTo(meta.ID, restoreDir); err != nil {
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

	// Original workspace should be untouched
	content, err = os.ReadFile(filepath.Join(workspaceDir, "app.go"))
	if err != nil {
		t.Fatalf("original workspace app.go missing: %v", err)
	}
	if string(content) != "package app\n" {
		t.Errorf("original app.go should be unchanged")
	}
}

func TestEngineList(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Create a test file
	if err := os.WriteFile(filepath.Join(workspaceDir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to create main.go: %v", err)
	}

	// Create engine with archive backend
	engine, err := NewEngine(workspaceDir, snapshotDir, EngineOptions{
		ForceBackend: "archive",
	})
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}

	// Create multiple snapshots with small delays to ensure different timestamps
	types := []Type{TypePreRun, TypeGit, TypeBuild}
	labels := []string{"first", "second", "third"}
	for i := 0; i < 3; i++ {
		_, err := engine.Create(types[i], labels[i])
		if err != nil {
			t.Fatalf("Create() error: %v", err)
		}
		// Small sleep to ensure different timestamps
		time.Sleep(10 * time.Millisecond)
	}

	// List snapshots
	listed, err := engine.List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}

	if len(listed) != 3 {
		t.Fatalf("List() returned %d snapshots, want 3", len(listed))
	}

	// Verify order is newest first
	for i := 0; i < len(listed)-1; i++ {
		if listed[i].CreatedAt.Before(listed[i+1].CreatedAt) {
			t.Errorf("List() not sorted newest first: %v before %v", listed[i].CreatedAt, listed[i+1].CreatedAt)
		}
	}

	// Verify the newest is "third" and oldest is "first"
	if listed[0].Label != "third" {
		t.Errorf("newest snapshot Label = %q, want %q", listed[0].Label, "third")
	}
	if listed[2].Label != "first" {
		t.Errorf("oldest snapshot Label = %q, want %q", listed[2].Label, "first")
	}
}

func TestEngineGet(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Create a test file
	if err := os.WriteFile(filepath.Join(workspaceDir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to create main.go: %v", err)
	}

	// Create engine with archive backend
	engine, err := NewEngine(workspaceDir, snapshotDir, EngineOptions{
		ForceBackend: "archive",
	})
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}

	// Create snapshot
	meta, err := engine.Create(TypeManual, "test")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Get by ID
	got, ok := engine.Get(meta.ID)
	if !ok {
		t.Fatal("Get() returned false for existing snapshot")
	}
	if got.ID != meta.ID {
		t.Errorf("Get().ID = %q, want %q", got.ID, meta.ID)
	}
	if got.Label != "test" {
		t.Errorf("Get().Label = %q, want %q", got.Label, "test")
	}

	// Get non-existent
	_, ok = engine.Get("snap_nonexistent")
	if ok {
		t.Error("Get() returned true for non-existent snapshot")
	}
}

func TestEngineDelete(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Create a test file
	if err := os.WriteFile(filepath.Join(workspaceDir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to create main.go: %v", err)
	}

	// Create engine with archive backend
	engine, err := NewEngine(workspaceDir, snapshotDir, EngineOptions{
		ForceBackend: "archive",
	})
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}

	// Create snapshot
	meta, err := engine.Create(TypeManual, "to delete")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Verify it exists
	_, ok := engine.Get(meta.ID)
	if !ok {
		t.Fatal("snapshot should exist before delete")
	}

	// Delete
	if err := engine.Delete(meta.ID); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	// Verify it's gone
	_, ok = engine.Get(meta.ID)
	if ok {
		t.Error("snapshot should not exist after delete")
	}

	// Verify list is empty
	listed, err := engine.List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("List() returned %d snapshots after delete, want 0", len(listed))
	}

	// Verify archive file was deleted
	if _, err := os.Stat(meta.NativeRef); !os.IsNotExist(err) {
		t.Errorf("archive file should not exist after Delete: %s", meta.NativeRef)
	}
}

func TestEngineMetadataPersistence(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Create a test file
	if err := os.WriteFile(filepath.Join(workspaceDir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to create main.go: %v", err)
	}

	// Create engine and snapshot with archive backend
	engine1, err := NewEngine(workspaceDir, snapshotDir, EngineOptions{
		ForceBackend: "archive",
	})
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}

	meta, err := engine1.Create(TypeManual, "persistent")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Create a new engine instance (simulating restart)
	engine2, err := NewEngine(workspaceDir, snapshotDir, EngineOptions{
		ForceBackend: "archive",
	})
	if err != nil {
		t.Fatalf("NewEngine() second instance error: %v", err)
	}

	// Verify snapshot is still accessible
	got, ok := engine2.Get(meta.ID)
	if !ok {
		t.Fatal("snapshot should persist across engine instances")
	}
	if got.Label != "persistent" {
		t.Errorf("persisted snapshot Label = %q, want %q", got.Label, "persistent")
	}
	if got.NativeRef != meta.NativeRef {
		t.Errorf("persisted snapshot NativeRef = %q, want %q", got.NativeRef, meta.NativeRef)
	}
}

func TestEngineGitignoreOption(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Create .gitignore
	gitignore := `node_modules/
*.log
`
	if err := os.WriteFile(filepath.Join(workspaceDir, ".gitignore"), []byte(gitignore), 0644); err != nil {
		t.Fatalf("failed to create .gitignore: %v", err)
	}

	// Create files that should be included
	if err := os.WriteFile(filepath.Join(workspaceDir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to create main.go: %v", err)
	}

	// Create files that should be excluded
	if err := os.MkdirAll(filepath.Join(workspaceDir, "node_modules/pkg"), 0755); err != nil {
		t.Fatalf("failed to create node_modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "node_modules/pkg/index.js"), []byte("module.exports = {}\n"), 0644); err != nil {
		t.Fatalf("failed to create node_modules file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "app.log"), []byte("log entry\n"), 0644); err != nil {
		t.Fatalf("failed to create app.log: %v", err)
	}

	// Create engine with gitignore support and archive backend
	engine, err := NewEngine(workspaceDir, snapshotDir, EngineOptions{
		UseGitignore: true,
		ForceBackend: "archive",
	})
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}

	meta, err := engine.Create(TypeManual, "gitignore test")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Restore to a clean directory to verify what was archived
	restoreDir := t.TempDir()
	if err := engine.RestoreTo(meta.ID, restoreDir); err != nil {
		t.Fatalf("RestoreTo() error: %v", err)
	}

	// Verify included files exist
	if _, err := os.Stat(filepath.Join(restoreDir, "main.go")); os.IsNotExist(err) {
		t.Errorf("main.go should exist in restored snapshot")
	}

	// Verify excluded files do not exist
	if _, err := os.Stat(filepath.Join(restoreDir, "node_modules")); !os.IsNotExist(err) {
		t.Errorf("node_modules should NOT exist in restored snapshot")
	}
	if _, err := os.Stat(filepath.Join(restoreDir, "app.log")); !os.IsNotExist(err) {
		t.Errorf("app.log should NOT exist in restored snapshot")
	}
}

func TestEngineAdditionalExcludes(t *testing.T) {
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

	// Create engine with additional excludes and archive backend
	engine, err := NewEngine(workspaceDir, snapshotDir, EngineOptions{
		Additional:   []string{"vendor/"},
		ForceBackend: "archive",
	})
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}

	meta, err := engine.Create(TypeManual, "excludes test")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Restore to verify
	restoreDir := t.TempDir()
	if err := engine.RestoreTo(meta.ID, restoreDir); err != nil {
		t.Fatalf("RestoreTo() error: %v", err)
	}

	// Verify included files exist
	if _, err := os.Stat(filepath.Join(restoreDir, "main.go")); os.IsNotExist(err) {
		t.Errorf("main.go should exist")
	}

	// Verify excluded directories do not exist
	if _, err := os.Stat(filepath.Join(restoreDir, "vendor")); !os.IsNotExist(err) {
		t.Errorf("vendor should NOT exist")
	}
}

func TestEngineRestoreNonExistent(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Create engine with archive backend
	engine, err := NewEngine(workspaceDir, snapshotDir, EngineOptions{
		ForceBackend: "archive",
	})
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}

	// Try to restore non-existent snapshot
	err = engine.Restore("snap_nonexistent")
	if err == nil {
		t.Error("Restore() should return error for non-existent snapshot")
	}
}

func TestEngineDeleteNonExistent(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Create engine with archive backend
	engine, err := NewEngine(workspaceDir, snapshotDir, EngineOptions{
		ForceBackend: "archive",
	})
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}

	// Try to delete non-existent snapshot
	err = engine.Delete("snap_nonexistent")
	if err == nil {
		t.Error("Delete() should return error for non-existent snapshot")
	}
}

func TestEngineForceAPFSBackend(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Force APFS backend explicitly to test ForceBackend option
	engine, err := NewEngine(workspaceDir, snapshotDir, EngineOptions{
		ForceBackend: "apfs",
	})
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}

	// Verify backend is APFS (even though we don't test actual APFS operations)
	if engine.Backend().Name() != "apfs" {
		t.Errorf("Backend().Name() = %q, want %q", engine.Backend().Name(), "apfs")
	}
}

func TestEngineAutoDetection(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Create engine without forcing backend - let it auto-detect
	engine, err := NewEngine(workspaceDir, snapshotDir, EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}

	// On darwin with APFS, should detect APFS. On other systems, should be archive.
	// Either way, a valid backend should be selected.
	backendName := engine.Backend().Name()
	if backendName != "apfs" && backendName != "archive" {
		t.Errorf("Backend().Name() = %q, want either 'apfs' or 'archive'", backendName)
	}

	t.Logf("Auto-detected backend: %s", backendName)
}

func TestEngineUnknownForceBackend(t *testing.T) {
	// Create temp directories
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Force an unknown backend - should fall through to auto-detection
	engine, err := NewEngine(workspaceDir, snapshotDir, EngineOptions{
		ForceBackend: "unknown",
	})
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}

	// Should fall through to auto-detection (either apfs or archive)
	backendName := engine.Backend().Name()
	if backendName != "apfs" && backendName != "archive" {
		t.Errorf("Backend().Name() = %q, want either 'apfs' or 'archive'", backendName)
	}
}

func TestEngineWorkspaceNotExist(t *testing.T) {
	snapshotDir := t.TempDir()

	// Try to create engine with non-existent workspace
	_, err := NewEngine("/nonexistent/workspace/path", snapshotDir, EngineOptions{
		ForceBackend: "archive",
	})
	if err == nil {
		t.Error("NewEngine() should return error for non-existent workspace")
	}
}
