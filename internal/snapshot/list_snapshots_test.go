package snapshot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestListSnapshotsMissing: a snapshot dir with no metadata file is not an error
// — it just means no snapshots have been taken yet.
func TestListSnapshotsMissing(t *testing.T) {
	got, err := ListSnapshots(t.TempDir())
	if err != nil {
		t.Fatalf("ListSnapshots on empty dir: unexpected error %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListSnapshots on empty dir: got %d entries, want 0", len(got))
	}
}

// TestListSnapshotsReadsStoreWithoutWorkspace is the companion to
// NewEngine(...).List(): ListSnapshots must return the stored snapshots even
// though no workspace path is provided or exists. This is what lets the
// destroy/clean extraction-snapshot guard work after the volume-mode workspace
// is gone.
func TestListSnapshotsReadsStoreWithoutWorkspace(t *testing.T) {
	dir := t.TempDir()
	writeSnapshotStore(t, dir,
		Metadata{ID: "snap_1", Type: TypePreRun, CreatedAt: time.Unix(1, 0)},
		Metadata{ID: "snap_2", Type: TypeManual, CreatedAt: time.Unix(2, 0)},
	)

	got, err := ListSnapshots(dir)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListSnapshots: got %d entries, want 2", len(got))
	}
}

// writeSnapshotStore writes a snapshots.json holding the given metadata into dir.
func writeSnapshotStore(t *testing.T, dir string, metas ...Metadata) {
	t.Helper()
	data, err := json.Marshal(metas)
	if err != nil {
		t.Fatalf("marshal snapshot metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, metadataFile), data, 0o600); err != nil {
		t.Fatalf("write snapshot store: %v", err)
	}
}
