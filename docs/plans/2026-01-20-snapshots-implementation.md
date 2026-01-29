# Snapshots & Execution Tracing Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Enable workspace protection through automatic snapshots and full command visibility via execution tracing.

**Architecture:** Hybrid snapshot backends (native filesystem when available, tar.gz fallback). Host-side execution tracing via eBPF (Linux) or Endpoint Security Framework (macOS). Event-based triggers derived from traced commands.

**Tech Stack:** Go, eBPF (cilium/ebpf), Apple Endpoint Security Framework (CGO), tar/gzip for archives.

**Design Reference:** `docs/plans/2026-01-20-snapshots-design.md`

---

## Phase 1: Foundation Types

### Task 1.1: Snapshot Types and Interface

**Files:**
- Create: `internal/snapshot/snapshot.go`
- Create: `internal/snapshot/snapshot_test.go`

**Step 1: Write the test for snapshot types**

```go
// internal/snapshot/snapshot_test.go
package snapshot

import (
	"testing"
	"time"
)

func TestSnapshotID(t *testing.T) {
	id := NewID()
	if len(id) != 12 { // "snap_" + 7 chars
		t.Errorf("expected ID length 12, got %d: %s", len(id), id)
	}
	if id[:5] != "snap_" {
		t.Errorf("expected prefix 'snap_', got %s", id[:5])
	}
}

func TestSnapshotTypes(t *testing.T) {
	tests := []struct {
		typ  Type
		want string
	}{
		{TypePreRun, "pre-run"},
		{TypeGit, "git"},
		{TypeBuild, "build"},
		{TypeIdle, "idle"},
		{TypeManual, "manual"},
		{TypeSafety, "safety"},
	}
	for _, tt := range tests {
		if tt.typ.String() != tt.want {
			t.Errorf("Type.String() = %s, want %s", tt.typ.String(), tt.want)
		}
	}
}

func TestSnapshotMetadata(t *testing.T) {
	meta := Metadata{
		ID:        "snap_abc123",
		Type:      TypePreRun,
		CreatedAt: time.Now(),
		Backend:   "apfs",
	}
	if meta.ID != "snap_abc123" {
		t.Errorf("unexpected ID: %s", meta.ID)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/snapshot/... -v`
Expected: FAIL - package doesn't exist

**Step 3: Write minimal implementation**

```go
// internal/snapshot/snapshot.go
package snapshot

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Type represents the trigger type for a snapshot.
type Type string

const (
	TypePreRun Type = "pre-run"
	TypeGit    Type = "git"
	TypeBuild  Type = "build"
	TypeIdle   Type = "idle"
	TypeManual Type = "manual"
	TypeSafety Type = "safety"
)

func (t Type) String() string {
	return string(t)
}

// Metadata describes a snapshot.
type Metadata struct {
	ID        string    `json:"id"`
	Type      Type      `json:"type"`
	Label     string    `json:"label,omitempty"`
	Backend   string    `json:"backend"`
	CreatedAt time.Time `json:"created_at"`
	SizeDelta *int64    `json:"size_delta,omitempty"`
	NativeRef string    `json:"native_ref,omitempty"`
}

// NewID generates a new snapshot ID in the format snap_<random>.
func NewID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return "snap_" + hex.EncodeToString(b)[:7]
}

// Backend defines the interface for snapshot storage backends.
type Backend interface {
	// Name returns the backend identifier (e.g., "apfs", "zfs", "archive").
	Name() string

	// Create creates a snapshot of the workspace and returns its native reference.
	Create(workspacePath string, id string) (nativeRef string, err error)

	// Restore restores a snapshot to the workspace (in-place).
	Restore(workspacePath string, nativeRef string) error

	// RestoreTo restores a snapshot to a different directory.
	RestoreTo(nativeRef string, destPath string) error

	// Delete removes a snapshot.
	Delete(nativeRef string) error

	// List returns all snapshots for a workspace.
	List(workspacePath string) ([]string, error)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/snapshot/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/snapshot/
git commit -m "feat(snapshot): add snapshot types and backend interface"
```

---

### Task 1.2: Execution Event Types

**Files:**
- Create: `internal/trace/event.go`
- Create: `internal/trace/event_test.go`

**Step 1: Write the test for exec event types**

```go
// internal/trace/event_test.go
package trace

import (
	"testing"
	"time"
)

func TestExecEvent(t *testing.T) {
	event := ExecEvent{
		Timestamp:  time.Now(),
		PID:        1234,
		PPID:       1,
		Command:    "git",
		Args:       []string{"commit", "-m", "test"},
		WorkingDir: "/workspace",
	}

	if event.Command != "git" {
		t.Errorf("unexpected command: %s", event.Command)
	}
	if len(event.Args) != 3 {
		t.Errorf("unexpected args length: %d", len(event.Args))
	}
}

func TestExecEventIsGitCommit(t *testing.T) {
	tests := []struct {
		name    string
		event   ExecEvent
		want    bool
	}{
		{
			name:    "git commit",
			event:   ExecEvent{Command: "git", Args: []string{"commit", "-m", "msg"}},
			want:    true,
		},
		{
			name:    "git status",
			event:   ExecEvent{Command: "git", Args: []string{"status"}},
			want:    false,
		},
		{
			name:    "not git",
			event:   ExecEvent{Command: "ls", Args: []string{"-la"}},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.event.IsGitCommit(); got != tt.want {
				t.Errorf("IsGitCommit() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExecEventIsBuildCommand(t *testing.T) {
	tests := []struct {
		name    string
		event   ExecEvent
		want    bool
	}{
		{
			name:    "npm run build",
			event:   ExecEvent{Command: "npm", Args: []string{"run", "build"}},
			want:    true,
		},
		{
			name:    "go build",
			event:   ExecEvent{Command: "go", Args: []string{"build", "./..."}},
			want:    true,
		},
		{
			name:    "make",
			event:   ExecEvent{Command: "make", Args: []string{}},
			want:    true,
		},
		{
			name:    "cargo build",
			event:   ExecEvent{Command: "cargo", Args: []string{"build"}},
			want:    true,
		},
		{
			name:    "not a build",
			event:   ExecEvent{Command: "ls", Args: []string{"-la"}},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.event.IsBuildCommand(); got != tt.want {
				t.Errorf("IsBuildCommand() = %v, want %v", got, tt.want)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/trace/... -v`
Expected: FAIL - package doesn't exist

**Step 3: Write minimal implementation**

```go
// internal/trace/event.go
package trace

import (
	"strings"
	"time"
)

// ExecEvent represents a command execution captured by the tracer.
type ExecEvent struct {
	Timestamp  time.Time      `json:"timestamp"`
	PID        int            `json:"pid"`
	PPID       int            `json:"ppid"`
	Command    string         `json:"command"`
	Args       []string       `json:"args"`
	WorkingDir string         `json:"working_dir,omitempty"`
	ExitCode   *int           `json:"exit_code,omitempty"`
	Duration   *time.Duration `json:"duration,omitempty"`
}

// IsGitCommit returns true if this event is a git commit command.
func (e ExecEvent) IsGitCommit() bool {
	if e.Command != "git" {
		return false
	}
	for _, arg := range e.Args {
		if arg == "commit" {
			return true
		}
	}
	return false
}

// IsBuildCommand returns true if this event is a build command.
func (e ExecEvent) IsBuildCommand() bool {
	// Check for common build commands
	buildCommands := map[string][]string{
		"npm":    {"run build", "run compile"},
		"yarn":   {"build"},
		"go":     {"build", "install"},
		"make":   {"", "all", "build"},
		"cargo":  {"build"},
		"mvn":    {"package", "compile"},
		"gradle": {"build"},
	}

	patterns, ok := buildCommands[e.Command]
	if !ok {
		return false
	}

	argsStr := strings.Join(e.Args, " ")
	for _, pattern := range patterns {
		if pattern == "" && len(e.Args) == 0 {
			return true // bare "make"
		}
		if strings.HasPrefix(argsStr, pattern) {
			return true
		}
	}
	return false
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/trace/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/trace/
git commit -m "feat(trace): add execution event types with git/build detection"
```

---

## Phase 2: Snapshot Backends

### Task 2.1: Archive Backend

**Files:**
- Create: `internal/snapshot/archive.go`
- Create: `internal/snapshot/archive_test.go`

**Step 1: Write the test for archive backend**

```go
// internal/snapshot/archive_test.go
package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveBackend(t *testing.T) {
	// Create temp workspace
	workspace := t.TempDir()
	snapshotDir := t.TempDir()

	// Create test file
	testFile := filepath.Join(workspace, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create gitignore to test exclusions
	gitignore := filepath.Join(workspace, ".gitignore")
	if err := os.WriteFile(gitignore, []byte("ignored/\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create ignored directory
	ignoredDir := filepath.Join(workspace, "ignored")
	if err := os.MkdirAll(ignoredDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ignoredDir, "secret.txt"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}

	backend := NewArchiveBackend(snapshotDir, ArchiveOptions{
		UseGitignore: true,
	})

	if backend.Name() != "archive" {
		t.Errorf("expected name 'archive', got %s", backend.Name())
	}

	// Create snapshot
	ref, err := backend.Create(workspace, "snap_test01")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Verify archive exists
	if _, err := os.Stat(ref); os.IsNotExist(err) {
		t.Errorf("archive not created at %s", ref)
	}

	// Modify workspace
	if err := os.WriteFile(testFile, []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}

	// Restore snapshot
	if err := backend.Restore(workspace, ref); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Verify content restored
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", content)
	}

	// Delete snapshot
	if err := backend.Delete(ref); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := os.Stat(ref); !os.IsNotExist(err) {
		t.Error("archive should be deleted")
	}
}

func TestArchiveBackendRestoreTo(t *testing.T) {
	workspace := t.TempDir()
	snapshotDir := t.TempDir()
	restoreDir := t.TempDir()

	// Create test file
	testFile := filepath.Join(workspace, "test.txt")
	if err := os.WriteFile(testFile, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	backend := NewArchiveBackend(snapshotDir, ArchiveOptions{})

	ref, err := backend.Create(workspace, "snap_test02")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Restore to different directory
	if err := backend.RestoreTo(ref, restoreDir); err != nil {
		t.Fatalf("RestoreTo failed: %v", err)
	}

	// Verify restored file
	content, err := os.ReadFile(filepath.Join(restoreDir, "test.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "original" {
		t.Errorf("expected 'original', got '%s'", content)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/snapshot/... -v -run Archive`
Expected: FAIL - NewArchiveBackend not defined

**Step 3: Write minimal implementation**

```go
// internal/snapshot/archive.go
package snapshot

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// ArchiveOptions configures the archive backend.
type ArchiveOptions struct {
	UseGitignore bool
	Additional   []string
}

// ArchiveBackend implements Backend using tar.gz archives.
type ArchiveBackend struct {
	snapshotDir string
	opts        ArchiveOptions
}

// NewArchiveBackend creates a new archive-based snapshot backend.
func NewArchiveBackend(snapshotDir string, opts ArchiveOptions) *ArchiveBackend {
	return &ArchiveBackend{
		snapshotDir: snapshotDir,
		opts:        opts,
	}
}

func (b *ArchiveBackend) Name() string {
	return "archive"
}

func (b *ArchiveBackend) Create(workspacePath string, id string) (string, error) {
	archivePath := filepath.Join(b.snapshotDir, id+".tar.gz")

	// Parse gitignore if enabled
	var matcher gitignore.Matcher
	if b.opts.UseGitignore {
		matcher = b.loadGitignore(workspacePath)
	}

	f, err := os.Create(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	err = filepath.Walk(workspacePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(workspacePath, path)
		if err != nil {
			return err
		}

		// Skip root
		if relPath == "." {
			return nil
		}

		// Check gitignore
		if matcher != nil {
			pathParts := strings.Split(relPath, string(filepath.Separator))
			if matcher.Match(pathParts, info.IsDir()) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		// Check additional excludes
		for _, pattern := range b.opts.Additional {
			if matched, _ := filepath.Match(pattern, relPath); matched {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = relPath

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			if _, err := io.Copy(tw, file); err != nil {
				return err
			}
		}

		return nil
	})

	return archivePath, err
}

func (b *ArchiveBackend) Restore(workspacePath string, nativeRef string) error {
	// Clear workspace first (preserve .git)
	entries, err := os.ReadDir(workspacePath)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".git" {
			continue
		}
		os.RemoveAll(filepath.Join(workspacePath, entry.Name()))
	}

	return b.extractArchive(nativeRef, workspacePath)
}

func (b *ArchiveBackend) RestoreTo(nativeRef string, destPath string) error {
	return b.extractArchive(nativeRef, destPath)
}

func (b *ArchiveBackend) Delete(nativeRef string) error {
	return os.Remove(nativeRef)
}

func (b *ArchiveBackend) List(workspacePath string) ([]string, error) {
	entries, err := os.ReadDir(b.snapshotDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return err
	}

	var refs []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tar.gz") {
			refs = append(refs, filepath.Join(b.snapshotDir, entry.Name()))
		}
	}
	return refs, nil
}

func (b *ArchiveBackend) extractArchive(archivePath, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destPath, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}

	return nil
}

func (b *ArchiveBackend) loadGitignore(workspacePath string) gitignore.Matcher {
	gitignorePath := filepath.Join(workspacePath, ".gitignore")
	f, err := os.Open(gitignorePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	patterns, err := gitignore.ReadPatterns(f, nil)
	if err != nil {
		return nil
	}

	return gitignore.NewMatcher(patterns)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/snapshot/... -v -run Archive`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/snapshot/archive.go internal/snapshot/archive_test.go
git commit -m "feat(snapshot): add archive backend with gitignore support"
```

---

### Task 2.2: APFS Backend (macOS)

**Files:**
- Create: `internal/snapshot/apfs_darwin.go`
- Create: `internal/snapshot/apfs_darwin_test.go`
- Create: `internal/snapshot/apfs_stub.go` (for non-darwin builds)

**Step 1: Write the test for APFS backend**

```go
// internal/snapshot/apfs_darwin_test.go
//go:build darwin

package snapshot

import (
	"os/exec"
	"testing"
)

func TestAPFSBackendName(t *testing.T) {
	backend := &APFSBackend{}
	if backend.Name() != "apfs" {
		t.Errorf("expected name 'apfs', got %s", backend.Name())
	}
}

func TestAPFSDetection(t *testing.T) {
	// This test checks if APFS detection works on the current system
	// It may skip if tmutil is not available
	if _, err := exec.LookPath("tmutil"); err != nil {
		t.Skip("tmutil not available")
	}

	// Test detection on a temp directory
	tmpDir := t.TempDir()
	isAPFS := IsAPFS(tmpDir)
	// We just verify it doesn't panic; actual result depends on system
	t.Logf("IsAPFS(%s) = %v", tmpDir, isAPFS)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/snapshot/... -v -run APFS`
Expected: FAIL - APFSBackend not defined

**Step 3: Write minimal implementation**

```go
// internal/snapshot/apfs_darwin.go
//go:build darwin

package snapshot

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// APFSBackend implements Backend using macOS APFS snapshots via tmutil.
type APFSBackend struct{}

// NewAPFSBackend creates a new APFS snapshot backend.
func NewAPFSBackend() *APFSBackend {
	return &APFSBackend{}
}

func (b *APFSBackend) Name() string {
	return "apfs"
}

func (b *APFSBackend) Create(workspacePath string, id string) (string, error) {
	// tmutil localsnapshot creates a snapshot and returns the name
	cmd := exec.Command("tmutil", "localsnapshot", workspacePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmutil localsnapshot failed: %w: %s", err, output)
	}

	// Parse output to get snapshot name
	// Output format: "Created local snapshot with date: 2026-01-20-103000"
	outputStr := strings.TrimSpace(string(output))
	parts := strings.Split(outputStr, ": ")
	if len(parts) < 2 {
		// Generate a timestamp-based name as fallback
		return fmt.Sprintf("com.apple.TimeMachine.%s.local", time.Now().Format("2006-01-02-150405")), nil
	}

	return parts[len(parts)-1], nil
}

func (b *APFSBackend) Restore(workspacePath string, nativeRef string) error {
	// APFS restore requires mounting the snapshot and copying
	// This is a simplified version - full implementation would use tmutil restore
	cmd := exec.Command("tmutil", "restore", nativeRef, workspacePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmutil restore failed: %w: %s", err, output)
	}
	return nil
}

func (b *APFSBackend) RestoreTo(nativeRef string, destPath string) error {
	cmd := exec.Command("tmutil", "restore", nativeRef, destPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmutil restore failed: %w: %s", err, output)
	}
	return nil
}

func (b *APFSBackend) Delete(nativeRef string) error {
	cmd := exec.Command("tmutil", "deletelocalsnapshots", nativeRef)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmutil deletelocalsnapshots failed: %w: %s", err, output)
	}
	return nil
}

func (b *APFSBackend) List(workspacePath string) ([]string, error) {
	cmd := exec.Command("tmutil", "listlocalsnapshots", workspacePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tmutil listlocalsnapshots failed: %w: %s", err, output)
	}

	var refs []string
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && strings.HasPrefix(line, "com.apple.TimeMachine") {
			refs = append(refs, line)
		}
	}
	return refs, nil
}

// IsAPFS returns true if the given path is on an APFS filesystem.
func IsAPFS(path string) bool {
	cmd := exec.Command("diskutil", "info", path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return bytes.Contains(output, []byte("Type (Bundle):             apfs"))
}
```

```go
// internal/snapshot/apfs_stub.go
//go:build !darwin

package snapshot

// APFSBackend is not available on non-darwin platforms.
type APFSBackend struct{}

func NewAPFSBackend() *APFSBackend {
	return nil
}

func (b *APFSBackend) Name() string                                       { return "apfs" }
func (b *APFSBackend) Create(workspacePath string, id string) (string, error) { return "", nil }
func (b *APFSBackend) Restore(workspacePath string, nativeRef string) error   { return nil }
func (b *APFSBackend) RestoreTo(nativeRef string, destPath string) error      { return nil }
func (b *APFSBackend) Delete(nativeRef string) error                          { return nil }
func (b *APFSBackend) List(workspacePath string) ([]string, error)            { return nil, nil }

// IsAPFS always returns false on non-darwin platforms.
func IsAPFS(path string) bool {
	return false
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/snapshot/... -v -run APFS`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/snapshot/apfs_darwin.go internal/snapshot/apfs_darwin_test.go internal/snapshot/apfs_stub.go
git commit -m "feat(snapshot): add APFS backend for macOS"
```

---

### Task 2.3: Backend Detection and Engine

**Files:**
- Create: `internal/snapshot/engine.go`
- Create: `internal/snapshot/engine_test.go`

**Step 1: Write the test for snapshot engine**

```go
// internal/snapshot/engine_test.go
package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEngineDetectsArchiveBackend(t *testing.T) {
	// On most test systems, temp dirs are on ext4/tmpfs, not ZFS/APFS
	workspace := t.TempDir()
	snapshotDir := filepath.Join(workspace, ".snapshots")

	engine, err := NewEngine(workspace, snapshotDir, EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}

	// On CI/test systems, we expect archive backend unless on macOS with APFS
	backendName := engine.Backend().Name()
	if backendName != "archive" && backendName != "apfs" {
		t.Errorf("unexpected backend: %s", backendName)
	}
}

func TestEngineCreateAndRestore(t *testing.T) {
	workspace := t.TempDir()
	snapshotDir := filepath.Join(workspace, ".snapshots")

	engine, err := NewEngine(workspace, snapshotDir, EngineOptions{
		ForceBackend: "archive", // Force archive for consistent testing
	})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}

	// Create test file
	testFile := filepath.Join(workspace, "data.txt")
	if err := os.WriteFile(testFile, []byte("version1"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create snapshot
	meta, err := engine.Create(TypePreRun, "")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if meta.Type != TypePreRun {
		t.Errorf("expected type pre-run, got %s", meta.Type)
	}

	// Modify file
	if err := os.WriteFile(testFile, []byte("version2"), 0644); err != nil {
		t.Fatal(err)
	}

	// Restore
	if err := engine.Restore(meta.ID); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Verify content restored
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "version1" {
		t.Errorf("expected 'version1', got '%s'", content)
	}
}

func TestEngineList(t *testing.T) {
	workspace := t.TempDir()
	snapshotDir := filepath.Join(workspace, ".snapshots")

	engine, err := NewEngine(workspace, snapshotDir, EngineOptions{
		ForceBackend: "archive",
	})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}

	// Create test file
	if err := os.WriteFile(filepath.Join(workspace, "test.txt"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create multiple snapshots
	_, err = engine.Create(TypePreRun, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Create(TypeManual, "checkpoint1")
	if err != nil {
		t.Fatal(err)
	}

	// List snapshots
	snapshots, err := engine.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(snapshots) != 2 {
		t.Errorf("expected 2 snapshots, got %d", len(snapshots))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/snapshot/... -v -run Engine`
Expected: FAIL - NewEngine not defined

**Step 3: Write minimal implementation**

```go
// internal/snapshot/engine.go
package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// EngineOptions configures the snapshot engine.
type EngineOptions struct {
	UseGitignore bool
	Additional   []string
	ForceBackend string // For testing: "archive", "apfs", "zfs", "btrfs"
}

// Engine manages snapshots for a workspace.
type Engine struct {
	workspace   string
	snapshotDir string
	backend     Backend
	opts        EngineOptions
	mu          sync.Mutex
	snapshots   map[string]Metadata
}

// NewEngine creates a new snapshot engine for the given workspace.
func NewEngine(workspace, snapshotDir string, opts EngineOptions) (*Engine, error) {
	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		return nil, fmt.Errorf("creating snapshot directory: %w", err)
	}

	e := &Engine{
		workspace:   workspace,
		snapshotDir: snapshotDir,
		opts:        opts,
		snapshots:   make(map[string]Metadata),
	}

	// Detect or use forced backend
	e.backend = e.detectBackend()

	// Load existing snapshot metadata
	if err := e.loadMetadata(); err != nil {
		return nil, fmt.Errorf("loading metadata: %w", err)
	}

	return e, nil
}

func (e *Engine) detectBackend() Backend {
	if e.opts.ForceBackend != "" {
		switch e.opts.ForceBackend {
		case "archive":
			return NewArchiveBackend(e.snapshotDir, ArchiveOptions{
				UseGitignore: e.opts.UseGitignore,
				Additional:   e.opts.Additional,
			})
		case "apfs":
			return NewAPFSBackend()
		}
	}

	// Auto-detect based on filesystem
	if runtime.GOOS == "darwin" && IsAPFS(e.workspace) {
		return NewAPFSBackend()
	}

	// TODO: Add ZFS and Btrfs detection

	// Default to archive
	return NewArchiveBackend(e.snapshotDir, ArchiveOptions{
		UseGitignore: e.opts.UseGitignore,
		Additional:   e.opts.Additional,
	})
}

// Backend returns the detected snapshot backend.
func (e *Engine) Backend() Backend {
	return e.backend
}

// Create creates a new snapshot.
func (e *Engine) Create(typ Type, label string) (Metadata, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	id := NewID()
	nativeRef, err := e.backend.Create(e.workspace, id)
	if err != nil {
		return Metadata{}, fmt.Errorf("creating snapshot: %w", err)
	}

	meta := Metadata{
		ID:        id,
		Type:      typ,
		Label:     label,
		Backend:   e.backend.Name(),
		CreatedAt: time.Now(),
		NativeRef: nativeRef,
	}

	e.snapshots[id] = meta

	if err := e.saveMetadata(); err != nil {
		return meta, fmt.Errorf("saving metadata: %w", err)
	}

	return meta, nil
}

// Restore restores a snapshot in-place.
func (e *Engine) Restore(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	meta, ok := e.snapshots[id]
	if !ok {
		return fmt.Errorf("snapshot not found: %s", id)
	}

	return e.backend.Restore(e.workspace, meta.NativeRef)
}

// RestoreTo restores a snapshot to a different directory.
func (e *Engine) RestoreTo(id, destPath string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	meta, ok := e.snapshots[id]
	if !ok {
		return fmt.Errorf("snapshot not found: %s", id)
	}

	return e.backend.RestoreTo(meta.NativeRef, destPath)
}

// Delete removes a snapshot.
func (e *Engine) Delete(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	meta, ok := e.snapshots[id]
	if !ok {
		return fmt.Errorf("snapshot not found: %s", id)
	}

	if err := e.backend.Delete(meta.NativeRef); err != nil {
		return fmt.Errorf("deleting snapshot: %w", err)
	}

	delete(e.snapshots, id)
	return e.saveMetadata()
}

// List returns all snapshots, ordered by creation time (newest first).
func (e *Engine) List() ([]Metadata, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var result []Metadata
	for _, meta := range e.snapshots {
		result = append(result, meta)
	}

	// Sort by creation time, newest first
	for i := 0; i < len(result)-1; i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].CreatedAt.Before(result[j].CreatedAt) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	return result, nil
}

// Get returns a snapshot by ID.
func (e *Engine) Get(id string) (Metadata, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	meta, ok := e.snapshots[id]
	return meta, ok
}

func (e *Engine) loadMetadata() error {
	metaPath := filepath.Join(e.snapshotDir, "snapshots.json")
	data, err := os.ReadFile(metaPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	var snapshots []Metadata
	if err := json.Unmarshal(data, &snapshots); err != nil {
		return err
	}

	for _, meta := range snapshots {
		e.snapshots[meta.ID] = meta
	}
	return nil
}

func (e *Engine) saveMetadata() error {
	var snapshots []Metadata
	for _, meta := range e.snapshots {
		snapshots = append(snapshots, meta)
	}

	data, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		return err
	}

	metaPath := filepath.Join(e.snapshotDir, "snapshots.json")
	return os.WriteFile(metaPath, data, 0644)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/snapshot/... -v -run Engine`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/snapshot/engine.go internal/snapshot/engine_test.go
git commit -m "feat(snapshot): add snapshot engine with backend detection"
```

---

## Phase 3: Configuration

### Task 3.1: Add Snapshot Config to agent.yaml

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Step 1: Write test for snapshot config parsing**

```go
// Add to internal/config/config_test.go

func TestSnapshotConfig(t *testing.T) {
	yaml := `
agent: test-agent
snapshots:
  disabled: false
  triggers:
    disable_pre_run: false
    disable_git_commits: true
    disable_builds: false
    disable_idle: false
    idle_threshold_seconds: 60
  exclude:
    ignore_gitignore: false
    additional:
      - "secrets/"
      - ".env.local"
  retention:
    max_count: 5
    delete_initial: false
tracing:
  disable_exec: false
`

	cfg, err := ParseConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}

	if cfg.Snapshots.Disabled {
		t.Error("expected snapshots enabled")
	}
	if !cfg.Snapshots.Triggers.DisableGitCommits {
		t.Error("expected git commits disabled")
	}
	if cfg.Snapshots.Triggers.IdleThresholdSeconds != 60 {
		t.Errorf("expected idle threshold 60, got %d", cfg.Snapshots.Triggers.IdleThresholdSeconds)
	}
	if len(cfg.Snapshots.Exclude.Additional) != 2 {
		t.Errorf("expected 2 additional excludes, got %d", len(cfg.Snapshots.Exclude.Additional))
	}
	if cfg.Snapshots.Retention.MaxCount != 5 {
		t.Errorf("expected max count 5, got %d", cfg.Snapshots.Retention.MaxCount)
	}
	if cfg.Tracing.DisableExec {
		t.Error("expected exec tracing enabled")
	}
}

func TestSnapshotConfigDefaults(t *testing.T) {
	yaml := `agent: test-agent`

	cfg, err := ParseConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}

	// All disabled flags should default to false (features enabled)
	if cfg.Snapshots.Disabled {
		t.Error("snapshots should be enabled by default")
	}
	if cfg.Snapshots.Triggers.DisablePreRun {
		t.Error("pre-run should be enabled by default")
	}
	if cfg.Snapshots.Triggers.IdleThresholdSeconds != 30 {
		t.Errorf("idle threshold should default to 30, got %d", cfg.Snapshots.Triggers.IdleThresholdSeconds)
	}
	if cfg.Snapshots.Retention.MaxCount != 10 {
		t.Errorf("max count should default to 10, got %d", cfg.Snapshots.Retention.MaxCount)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/config/... -v -run Snapshot`
Expected: FAIL - Snapshots field doesn't exist

**Step 3: Modify config.go to add snapshot config**

Add these types to `internal/config/config.go`:

```go
// SnapshotConfig configures workspace snapshots.
type SnapshotConfig struct {
	Disabled  bool                   `yaml:"disabled"`
	Triggers  SnapshotTriggerConfig  `yaml:"triggers"`
	Exclude   SnapshotExcludeConfig  `yaml:"exclude"`
	Retention SnapshotRetentionConfig `yaml:"retention"`
}

// SnapshotTriggerConfig configures when snapshots are created.
type SnapshotTriggerConfig struct {
	DisablePreRun      bool `yaml:"disable_pre_run"`
	DisableGitCommits  bool `yaml:"disable_git_commits"`
	DisableBuilds      bool `yaml:"disable_builds"`
	DisableIdle        bool `yaml:"disable_idle"`
	IdleThresholdSeconds int `yaml:"idle_threshold_seconds"`
}

// SnapshotExcludeConfig configures what to exclude from snapshots.
type SnapshotExcludeConfig struct {
	IgnoreGitignore bool     `yaml:"ignore_gitignore"`
	Additional      []string `yaml:"additional"`
}

// SnapshotRetentionConfig configures snapshot retention.
type SnapshotRetentionConfig struct {
	MaxCount      int  `yaml:"max_count"`
	DeleteInitial bool `yaml:"delete_initial"`
}

// TracingConfig configures execution tracing.
type TracingConfig struct {
	DisableExec bool `yaml:"disable_exec"`
}
```

Add fields to Config struct:

```go
type Config struct {
	// ... existing fields ...
	Snapshots SnapshotConfig `yaml:"snapshots"`
	Tracing   TracingConfig  `yaml:"tracing"`
}
```

Add defaults in validation/parsing:

```go
func (c *Config) applyDefaults() {
	// ... existing defaults ...

	// Snapshot defaults
	if c.Snapshots.Triggers.IdleThresholdSeconds == 0 {
		c.Snapshots.Triggers.IdleThresholdSeconds = 30
	}
	if c.Snapshots.Retention.MaxCount == 0 {
		c.Snapshots.Retention.MaxCount = 10
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/config/... -v -run Snapshot`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add snapshot and tracing configuration"
```

---

## Phase 4: Storage Integration

### Task 4.1: Add Exec Event Storage

**Files:**
- Modify: `internal/storage/storage.go`
- Modify: `internal/storage/storage_test.go`

**Step 1: Write test for exec event storage**

```go
// Add to internal/storage/storage_test.go

func TestWriteExecEvent(t *testing.T) {
	store, err := NewRunStore(t.TempDir(), "test-run")
	if err != nil {
		t.Fatal(err)
	}

	event := ExecEvent{
		Timestamp:  time.Now(),
		PID:        1234,
		PPID:       1,
		Command:    "git",
		Args:       []string{"commit", "-m", "test"},
		WorkingDir: "/workspace",
	}

	if err := store.WriteExecEvent(event); err != nil {
		t.Fatalf("WriteExecEvent failed: %v", err)
	}

	events, err := store.ReadExecEvents()
	if err != nil {
		t.Fatalf("ReadExecEvents failed: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Command != "git" {
		t.Errorf("expected command 'git', got '%s'", events[0].Command)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/storage/... -v -run ExecEvent`
Expected: FAIL - WriteExecEvent not defined

**Step 3: Add exec event storage methods**

Add to `internal/storage/storage.go`:

```go
// ExecEvent mirrors trace.ExecEvent for storage.
type ExecEvent struct {
	Timestamp  time.Time      `json:"timestamp"`
	PID        int            `json:"pid"`
	PPID       int            `json:"ppid"`
	Command    string         `json:"command"`
	Args       []string       `json:"args"`
	WorkingDir string         `json:"working_dir,omitempty"`
	ExitCode   *int           `json:"exit_code,omitempty"`
	Duration   *time.Duration `json:"duration,omitempty"`
}

// WriteExecEvent writes an execution event to exec.jsonl.
func (s *RunStore) WriteExecEvent(event ExecEvent) error {
	return s.appendJSONL("exec.jsonl", event)
}

// ReadExecEvents reads all execution events from exec.jsonl.
func (s *RunStore) ReadExecEvents() ([]ExecEvent, error) {
	var events []ExecEvent
	err := s.readJSONL("exec.jsonl", func(line []byte) error {
		var event ExecEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return err
		}
		events = append(events, event)
		return nil
	})
	return events, err
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/storage/... -v -run ExecEvent`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/storage/storage.go internal/storage/storage_test.go
git commit -m "feat(storage): add execution event storage"
```

---

## Phase 5: CLI Commands

### Task 5.1: Add `moat snapshots` Command

**Files:**
- Create: `cmd/moat/cli/snapshots.go`

**Step 1: Write the snapshots list command**

```go
// cmd/moat/cli/snapshots.go
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/majorcontext/moat/internal/snapshot"
	"github.com/majorcontext/moat/internal/storage"
	"github.com/spf13/cobra"
)

func newSnapshotsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshots <run-id>",
		Short: "List snapshots for a run",
		Args:  cobra.ExactArgs(1),
		RunE:  runSnapshots,
	}

	cmd.AddCommand(newSnapshotsPruneCmd())

	return cmd
}

func runSnapshots(cmd *cobra.Command, args []string) error {
	runID := args[0]

	baseDir := storage.DefaultBaseDir()
	runDir := filepath.Join(baseDir, runID)

	if _, err := os.Stat(runDir); os.IsNotExist(err) {
		return fmt.Errorf("run not found: %s", runID)
	}

	snapshotDir := filepath.Join(runDir, "snapshots")
	engine, err := snapshot.NewEngine("", snapshotDir, snapshot.EngineOptions{})
	if err != nil {
		return fmt.Errorf("loading snapshots: %w", err)
	}

	snapshots, err := engine.List()
	if err != nil {
		return fmt.Errorf("listing snapshots: %w", err)
	}

	if len(snapshots) == 0 {
		fmt.Println("No snapshots found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTYPE\tLABEL\tCREATED")

	for _, snap := range snapshots {
		label := snap.Label
		if label == "" {
			label = "-"
		}
		age := formatAge(snap.CreatedAt)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", snap.ID, snap.Type, label, age)
	}

	w.Flush()
	fmt.Printf("\n%d snapshots (backend: %s)\n", len(snapshots), engine.Backend().Name())

	return nil
}

func newSnapshotsPruneCmd() *cobra.Command {
	var keep int

	cmd := &cobra.Command{
		Use:   "prune <run-id>",
		Short: "Delete old snapshots",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSnapshotsPrune(args[0], keep)
		},
	}

	cmd.Flags().IntVar(&keep, "keep", 5, "Number of snapshots to keep")

	return cmd
}

func runSnapshotsPrune(runID string, keep int) error {
	baseDir := storage.DefaultBaseDir()
	runDir := filepath.Join(baseDir, runID)
	snapshotDir := filepath.Join(runDir, "snapshots")

	engine, err := snapshot.NewEngine("", snapshotDir, snapshot.EngineOptions{})
	if err != nil {
		return fmt.Errorf("loading snapshots: %w", err)
	}

	snapshots, err := engine.List()
	if err != nil {
		return fmt.Errorf("listing snapshots: %w", err)
	}

	if len(snapshots) <= keep {
		fmt.Printf("Only %d snapshots, nothing to prune.\n", len(snapshots))
		return nil
	}

	// Keep the newest N, delete the rest (but never delete pre-run)
	toDelete := snapshots[keep:]
	deleted := 0

	for _, snap := range toDelete {
		if snap.Type == snapshot.TypePreRun {
			continue // Never auto-delete pre-run
		}
		if err := engine.Delete(snap.ID); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to delete %s: %v\n", snap.ID, err)
			continue
		}
		deleted++
	}

	fmt.Printf("Deleted %d snapshots.\n", deleted)
	return nil
}

func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}
```

**Step 2: Register command in root.go**

Add to the root command setup:

```go
rootCmd.AddCommand(newSnapshotsCmd())
```

**Step 3: Build and test manually**

Run: `go build ./cmd/moat && ./moat snapshots --help`
Expected: Shows help for snapshots command

**Step 4: Commit**

```bash
git add cmd/moat/cli/snapshots.go
git commit -m "feat(cli): add moat snapshots command"
```

---

### Task 5.2: Add `moat snapshot` Command (Create)

**Files:**
- Create: `cmd/moat/cli/snapshot.go`

**Step 1: Write the snapshot create command**

```go
// cmd/moat/cli/snapshot.go
package cli

import (
	"fmt"
	"path/filepath"

	"github.com/majorcontext/moat/internal/snapshot"
	"github.com/majorcontext/moat/internal/storage"
	"github.com/spf13/cobra"
)

func newSnapshotCmd() *cobra.Command {
	var label string

	cmd := &cobra.Command{
		Use:   "snapshot <run-id>",
		Short: "Create a manual snapshot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSnapshot(args[0], label)
		},
	}

	cmd.Flags().StringVar(&label, "label", "", "Optional label for the snapshot")

	return cmd
}

func runSnapshot(runID, label string) error {
	baseDir := storage.DefaultBaseDir()
	runDir := filepath.Join(baseDir, runID)
	snapshotDir := filepath.Join(runDir, "snapshots")

	// Load run metadata to get workspace path
	store, err := storage.NewRunStore(baseDir, runID)
	if err != nil {
		return fmt.Errorf("loading run: %w", err)
	}

	meta, err := store.LoadMetadata()
	if err != nil {
		return fmt.Errorf("loading run metadata: %w", err)
	}

	engine, err := snapshot.NewEngine(meta.Workspace, snapshotDir, snapshot.EngineOptions{
		UseGitignore: true,
	})
	if err != nil {
		return fmt.Errorf("initializing snapshot engine: %w", err)
	}

	snap, err := engine.Create(snapshot.TypeManual, label)
	if err != nil {
		return fmt.Errorf("creating snapshot: %w", err)
	}

	if label != "" {
		fmt.Printf("%s (%s)\n", snap.ID, label)
	} else {
		fmt.Println(snap.ID)
	}

	return nil
}
```

**Step 2: Register command in root.go**

```go
rootCmd.AddCommand(newSnapshotCmd())
```

**Step 3: Build and test manually**

Run: `go build ./cmd/moat && ./moat snapshot --help`
Expected: Shows help for snapshot command

**Step 4: Commit**

```bash
git add cmd/moat/cli/snapshot.go
git commit -m "feat(cli): add moat snapshot command for manual snapshots"
```

---

### Task 5.3: Add `moat rollback` Command

**Files:**
- Create: `cmd/moat/cli/rollback.go`

**Step 1: Write the rollback command**

```go
// cmd/moat/cli/rollback.go
package cli

import (
	"fmt"
	"path/filepath"

	"github.com/majorcontext/moat/internal/snapshot"
	"github.com/majorcontext/moat/internal/storage"
	"github.com/spf13/cobra"
)

func newRollbackCmd() *cobra.Command {
	var toPath string

	cmd := &cobra.Command{
		Use:   "rollback <run-id> [snapshot-id]",
		Short: "Restore workspace from a snapshot",
		Long: `Restore workspace from a snapshot. If no snapshot-id is provided,
restores from the most recent snapshot.

By default, restores in-place (replacing current workspace contents).
Use --to to restore to a different directory.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			runID := args[0]
			var snapID string
			if len(args) > 1 {
				snapID = args[1]
			}
			return runRollback(runID, snapID, toPath)
		},
	}

	cmd.Flags().StringVar(&toPath, "to", "", "Restore to a different directory")

	return cmd
}

func runRollback(runID, snapID, toPath string) error {
	baseDir := storage.DefaultBaseDir()
	runDir := filepath.Join(baseDir, runID)
	snapshotDir := filepath.Join(runDir, "snapshots")

	// Load run metadata to get workspace path
	store, err := storage.NewRunStore(baseDir, runID)
	if err != nil {
		return fmt.Errorf("loading run: %w", err)
	}

	meta, err := store.LoadMetadata()
	if err != nil {
		return fmt.Errorf("loading run metadata: %w", err)
	}

	engine, err := snapshot.NewEngine(meta.Workspace, snapshotDir, snapshot.EngineOptions{
		UseGitignore: true,
	})
	if err != nil {
		return fmt.Errorf("initializing snapshot engine: %w", err)
	}

	// If no snapshot ID provided, use the most recent
	if snapID == "" {
		snapshots, err := engine.List()
		if err != nil {
			return fmt.Errorf("listing snapshots: %w", err)
		}
		if len(snapshots) == 0 {
			return fmt.Errorf("no snapshots found for run %s", runID)
		}
		snapID = snapshots[0].ID
	}

	// Verify snapshot exists
	targetSnap, ok := engine.Get(snapID)
	if !ok {
		return fmt.Errorf("snapshot not found: %s", snapID)
	}

	if toPath != "" {
		// Restore to different directory
		fmt.Printf("Extracting snapshot to %s... ", toPath)
		if err := engine.RestoreTo(snapID, toPath); err != nil {
			return fmt.Errorf("restore failed: %w", err)
		}
		fmt.Println("done")
	} else {
		// In-place restore: create safety snapshot first
		fmt.Print("Creating safety snapshot of current state... ")
		safety, err := engine.Create(snapshot.TypeSafety, "")
		if err != nil {
			return fmt.Errorf("creating safety snapshot: %w", err)
		}
		fmt.Printf("done (%s)\n", safety.ID)

		fmt.Printf("Restoring workspace to %s... ", snapID)
		if err := engine.Restore(snapID); err != nil {
			return fmt.Errorf("restore failed: %w", err)
		}
		fmt.Println("done")

		fmt.Printf("\nTo undo: moat rollback %s %s\n", runID, safety.ID)
	}

	_ = targetSnap // Used for potential future enhancements

	return nil
}
```

**Step 2: Register command in root.go**

```go
rootCmd.AddCommand(newRollbackCmd())
```

**Step 3: Build and test manually**

Run: `go build ./cmd/moat && ./moat rollback --help`
Expected: Shows help for rollback command

**Step 4: Commit**

```bash
git add cmd/moat/cli/rollback.go
git commit -m "feat(cli): add moat rollback command with safety snapshots"
```

---

## Phase 6: Run Lifecycle Integration

### Task 6.1: Integrate Snapshot Engine into Run Manager

**Files:**
- Modify: `internal/run/run.go`
- Modify: `internal/run/manager.go`

**Step 1: Add snapshot engine to Run struct**

Add to `internal/run/run.go`:

```go
import "github.com/majorcontext/moat/internal/snapshot"

// Add to Run struct:
snapEngine *snapshot.Engine
```

**Step 2: Initialize snapshot engine in manager.Create**

Add to `internal/run/manager.go` in the Create function, after storage is initialized:

```go
// Initialize snapshot engine if not disabled
if !cfg.Snapshots.Disabled {
    snapshotDir := filepath.Join(run.store.Dir(), "snapshots")
    run.snapEngine, err = snapshot.NewEngine(workspacePath, snapshotDir, snapshot.EngineOptions{
        UseGitignore: !cfg.Snapshots.Exclude.IgnoreGitignore,
        Additional:   cfg.Snapshots.Exclude.Additional,
    })
    if err != nil {
        return nil, fmt.Errorf("initializing snapshot engine: %w", err)
    }
}
```

**Step 3: Create pre-run snapshot in manager.Start**

Add to `internal/run/manager.go` in the Start function, after container starts:

```go
// Create pre-run snapshot
if run.snapEngine != nil && !cfg.Snapshots.Triggers.DisablePreRun {
    if _, err := run.snapEngine.Create(snapshot.TypePreRun, ""); err != nil {
        // Log but don't fail - snapshots are best-effort
        slog.Warn("failed to create pre-run snapshot", "error", err)
    }
}
```

**Step 4: Build and test**

Run: `go build ./cmd/moat`
Expected: Builds successfully

**Step 5: Commit**

```bash
git add internal/run/run.go internal/run/manager.go
git commit -m "feat(run): integrate snapshot engine into run lifecycle"
```

---

## Phase 7: Execution Tracer (Stub)

### Task 7.1: Create Tracer Interface and Stub

**Files:**
- Create: `internal/trace/tracer.go`
- Create: `internal/trace/tracer_stub.go`

**Step 1: Write the tracer interface**

```go
// internal/trace/tracer.go
package trace

// Tracer captures command executions inside a container.
type Tracer interface {
	// Start begins tracing.
	Start() error

	// Stop ends tracing.
	Stop() error

	// Events returns a channel of execution events.
	Events() <-chan ExecEvent

	// OnExec registers a callback for execution events.
	OnExec(func(ExecEvent))
}

// Config configures the tracer.
type Config struct {
	CgroupPath string // Linux: cgroup path for the container
	PID        int    // Process ID to trace (and children)
}
```

**Step 2: Write the stub tracer**

```go
// internal/trace/tracer_stub.go
package trace

import "log/slog"

// StubTracer is a no-op tracer for platforms without native tracing support.
type StubTracer struct {
	events    chan ExecEvent
	callbacks []func(ExecEvent)
}

// NewStubTracer creates a stub tracer that does nothing.
func NewStubTracer(cfg Config) *StubTracer {
	return &StubTracer{
		events: make(chan ExecEvent, 100),
	}
}

func (t *StubTracer) Start() error {
	slog.Debug("stub tracer started (no-op)")
	return nil
}

func (t *StubTracer) Stop() error {
	close(t.events)
	return nil
}

func (t *StubTracer) Events() <-chan ExecEvent {
	return t.events
}

func (t *StubTracer) OnExec(cb func(ExecEvent)) {
	t.callbacks = append(t.callbacks, cb)
}

// Emit allows manual event injection for testing.
func (t *StubTracer) Emit(event ExecEvent) {
	for _, cb := range t.callbacks {
		cb(event)
	}
	select {
	case t.events <- event:
	default:
	}
}
```

**Step 3: Write test for stub tracer**

```go
// internal/trace/tracer_test.go
package trace

import (
	"testing"
	"time"
)

func TestStubTracer(t *testing.T) {
	tracer := NewStubTracer(Config{})

	var received []ExecEvent
	tracer.OnExec(func(e ExecEvent) {
		received = append(received, e)
	})

	if err := tracer.Start(); err != nil {
		t.Fatal(err)
	}

	// Emit test event
	tracer.Emit(ExecEvent{
		Timestamp: time.Now(),
		PID:       1234,
		Command:   "test",
	})

	if len(received) != 1 {
		t.Errorf("expected 1 event, got %d", len(received))
	}

	if err := tracer.Stop(); err != nil {
		t.Fatal(err)
	}
}
```

**Step 4: Run tests**

Run: `go test ./internal/trace/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/trace/
git commit -m "feat(trace): add tracer interface and stub implementation"
```

---

## Summary

This implementation plan covers:

1. **Phase 1:** Foundation types (snapshot metadata, exec events)
2. **Phase 2:** Snapshot backends (archive, APFS, engine)
3. **Phase 3:** Configuration (agent.yaml schema)
4. **Phase 4:** Storage integration (exec event persistence)
5. **Phase 5:** CLI commands (snapshots, snapshot, rollback)
6. **Phase 6:** Run lifecycle integration
7. **Phase 7:** Execution tracer stub

### Future Work (Not in This Plan)

- Linux eBPF tracer implementation
- macOS Endpoint Security Framework tracer
- ZFS and Btrfs backends
- Idle detection trigger
- Build completion trigger based on exec events

### Dependencies

Add to `go.mod`:
```
github.com/go-git/go-git/v5 v5.x.x  // For gitignore parsing
```
