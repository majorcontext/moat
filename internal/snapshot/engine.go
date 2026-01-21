package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// EngineOptions configures the snapshot engine behavior.
type EngineOptions struct {
	// UseGitignore enables parsing .gitignore files to exclude matching paths.
	UseGitignore bool
	// Additional specifies extra patterns to exclude (gitignore syntax).
	Additional []string
	// ForceBackend forces a specific backend for testing: "archive" or "apfs".
	ForceBackend string
}

// Engine manages snapshot operations with automatic backend detection.
type Engine struct {
	workspace   string
	snapshotDir string
	backend     Backend
	opts        EngineOptions
	mu          sync.Mutex
	snapshots   map[string]Metadata
}

// metadataFile is the filename for persisted snapshot metadata.
const metadataFile = "snapshots.json"

// NewEngine creates a new snapshot engine with automatic backend detection.
// It loads any existing snapshot metadata from the snapshot directory.
func NewEngine(workspace, snapshotDir string, opts EngineOptions) (*Engine, error) {
	// Ensure workspace exists
	if _, err := os.Stat(workspace); os.IsNotExist(err) {
		return nil, fmt.Errorf("workspace does not exist: %s", workspace)
	}

	// Ensure snapshot directory exists
	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		return nil, fmt.Errorf("create snapshot directory: %w", err)
	}

	// Detect backend
	backend := detectBackend(workspace, snapshotDir, opts)

	engine := &Engine{
		workspace:   workspace,
		snapshotDir: snapshotDir,
		backend:     backend,
		opts:        opts,
		snapshots:   make(map[string]Metadata),
	}

	// Load existing metadata
	if err := engine.loadMetadata(); err != nil {
		return nil, fmt.Errorf("load metadata: %w", err)
	}

	return engine, nil
}

// detectBackend selects the appropriate backend based on options and filesystem.
func detectBackend(workspace, snapshotDir string, opts EngineOptions) Backend {
	// If ForceBackend is set, use that
	if opts.ForceBackend != "" {
		switch opts.ForceBackend {
		case "apfs":
			return NewAPFSBackend(snapshotDir)
		case "archive":
			return NewArchiveBackend(snapshotDir, ArchiveOptions{
				UseGitignore: opts.UseGitignore,
				Additional:   opts.Additional,
			})
		default:
			// Unknown backend, fall through to auto-detection
		}
	}

	// Auto-detect: use APFS backend on macOS when workspace is on APFS,
	// otherwise fall back to archive backend
	if IsAPFS(workspace) {
		return NewAPFSBackend(snapshotDir)
	}

	// Default to archive backend for non-APFS filesystems
	return NewArchiveBackend(snapshotDir, ArchiveOptions{
		UseGitignore: opts.UseGitignore,
		Additional:   opts.Additional,
	})
}

// Backend returns the detected backend.
func (e *Engine) Backend() Backend {
	return e.backend
}

// Create creates a new snapshot with the given type and label.
func (e *Engine) Create(typ Type, label string) (Metadata, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	id := NewID()
	nativeRef, err := e.backend.Create(e.workspace, id)
	if err != nil {
		return Metadata{}, fmt.Errorf("backend create: %w", err)
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
		// Clean up the snapshot if we can't save metadata
		_ = e.backend.Delete(nativeRef)
		delete(e.snapshots, id)
		return Metadata{}, fmt.Errorf("save metadata: %w", err)
	}

	return meta, nil
}

// Restore restores a snapshot in-place to the workspace.
func (e *Engine) Restore(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	meta, ok := e.snapshots[id]
	if !ok {
		return fmt.Errorf("snapshot not found: %s", id)
	}

	if err := e.backend.Restore(e.workspace, meta.NativeRef); err != nil {
		return fmt.Errorf("backend restore: %w", err)
	}

	return nil
}

// RestoreTo restores a snapshot to a different directory.
func (e *Engine) RestoreTo(id, destPath string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	meta, ok := e.snapshots[id]
	if !ok {
		return fmt.Errorf("snapshot not found: %s", id)
	}

	if err := e.backend.RestoreTo(meta.NativeRef, destPath); err != nil {
		return fmt.Errorf("backend restore to: %w", err)
	}

	return nil
}

// Delete removes a snapshot and its metadata.
func (e *Engine) Delete(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	meta, ok := e.snapshots[id]
	if !ok {
		return fmt.Errorf("snapshot not found: %s", id)
	}

	if err := e.backend.Delete(meta.NativeRef); err != nil {
		return fmt.Errorf("backend delete: %w", err)
	}

	delete(e.snapshots, id)

	if err := e.saveMetadata(); err != nil {
		return fmt.Errorf("save metadata: %w", err)
	}

	return nil
}

// List returns all snapshots sorted by creation time (newest first).
func (e *Engine) List() ([]Metadata, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	list := make([]Metadata, 0, len(e.snapshots))
	for _, meta := range e.snapshots {
		list = append(list, meta)
	}

	// Sort by creation time, newest first
	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})

	return list, nil
}

// Get returns a snapshot by ID.
func (e *Engine) Get(id string) (Metadata, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	meta, ok := e.snapshots[id]
	return meta, ok
}

// loadMetadata loads snapshot metadata from the metadata file.
func (e *Engine) loadMetadata() error {
	path := filepath.Join(e.snapshotDir, metadataFile)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// No metadata file yet, that's OK
			return nil
		}
		return fmt.Errorf("read metadata file: %w", err)
	}

	var list []Metadata
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("corrupted snapshot metadata at %s: %w\nTo reset, delete the file: rm %s", path, err, path)
	}

	for _, meta := range list {
		e.snapshots[meta.ID] = meta
	}

	return nil
}

// saveMetadata saves snapshot metadata to the metadata file.
func (e *Engine) saveMetadata() error {
	list := make([]Metadata, 0, len(e.snapshots))
	for _, meta := range e.snapshots {
		list = append(list, meta)
	}

	// Sort by creation time for consistent file ordering
	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	path := filepath.Join(e.snapshotDir, metadataFile)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write metadata file: %w", err)
	}

	return nil
}
