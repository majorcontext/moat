// Package snapshot provides types and interfaces for workspace snapshots.
package snapshot

import (
	"time"

	"github.com/majorcontext/moat/internal/id"
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

// NewID generates a new snapshot ID using id.Generate with the "snap" prefix,
// producing values like "snap_a1b2c3d4e5f6" (12-character hex suffix).
func NewID() string {
	return id.Generate("snap")
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
