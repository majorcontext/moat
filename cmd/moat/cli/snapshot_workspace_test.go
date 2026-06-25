package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSnapshotEngineWorkspace pins the fix for snapshot list/prune/extract-to
// failing when the original workspace dir is gone: an existing path is returned
// as-is (so backend auto-detection still sees the real FS), while a missing or
// empty path falls back to a guaranteed-existing placeholder.
func TestSnapshotEngineWorkspace(t *testing.T) {
	existing := t.TempDir()
	if got := snapshotEngineWorkspace(existing); got != existing {
		t.Errorf("existing workspace: got %q, want %q", got, existing)
	}

	missing := filepath.Join(existing, "gone")
	if got := snapshotEngineWorkspace(missing); got != os.TempDir() {
		t.Errorf("missing workspace: got %q, want os.TempDir() %q", got, os.TempDir())
	}
	if got := snapshotEngineWorkspace(""); got != os.TempDir() {
		t.Errorf("empty workspace: got %q, want os.TempDir() %q", got, os.TempDir())
	}

	// The placeholder must actually exist — that is the whole point, since
	// snapshot.NewEngine rejects a non-existent workspace path.
	if _, err := os.Stat(snapshotEngineWorkspace(missing)); err != nil {
		t.Errorf("placeholder dir must exist: %v", err)
	}
}
