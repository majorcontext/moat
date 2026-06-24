package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/snapshot"
	"github.com/majorcontext/moat/internal/storage"
)

// TestHasExtractionSnapshot pins the destroy/clean data-loss guard: a run counts
// as having an extraction snapshot only when a non-(pre-run|safety) snapshot
// exists. The function must answer correctly from the snapshot store alone — no
// run metadata.json and no live workspace are written here, mirroring a
// volume-mode run whose host workspace is already gone by destroy time.
func TestHasExtractionSnapshot(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())

	tests := []struct {
		name  string
		runID string
		metas []snapshot.Metadata // nil means "no snapshots dir at all"
		want  bool
	}{
		{name: "no snapshot store", runID: "run_none", metas: nil, want: false},
		{
			name:  "pre-run only",
			runID: "run_prerun",
			metas: []snapshot.Metadata{{ID: "snap_1", Type: snapshot.TypePreRun, CreatedAt: time.Unix(1, 0)}},
			want:  false,
		},
		{
			name:  "pre-run plus safety",
			runID: "run_safety",
			metas: []snapshot.Metadata{
				{ID: "snap_1", Type: snapshot.TypePreRun, CreatedAt: time.Unix(1, 0)},
				{ID: "snap_2", Type: snapshot.TypeSafety, CreatedAt: time.Unix(2, 0)},
			},
			want: false,
		},
		{
			name:  "manual extraction snapshot present",
			runID: "run_manual",
			metas: []snapshot.Metadata{
				{ID: "snap_1", Type: snapshot.TypePreRun, CreatedAt: time.Unix(1, 0)},
				{ID: "snap_2", Type: snapshot.TypeManual, CreatedAt: time.Unix(2, 0)},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.metas != nil {
				writeRunSnapshots(t, tt.runID, tt.metas)
			}
			if got := hasExtractionSnapshot(tt.runID); got != tt.want {
				t.Errorf("hasExtractionSnapshot(%q) = %v, want %v", tt.runID, got, tt.want)
			}
		})
	}
}

// writeRunSnapshots writes a snapshots.json for runID under the configured
// base dir. It deliberately does NOT write metadata.json or create the run's
// workspace, so the test also guards against hasExtractionSnapshot regressing to
// a workspace-dependent implementation that would fail closed.
func writeRunSnapshots(t *testing.T, runID string, metas []snapshot.Metadata) {
	t.Helper()
	snapDir := filepath.Join(storage.DefaultBaseDir(), runID, "snapshots")
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		t.Fatalf("mkdir snapshots dir: %v", err)
	}
	data, err := json.Marshal(metas)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(snapDir, "snapshots.json"), data, 0o600); err != nil {
		t.Fatalf("write snapshots.json: %v", err)
	}
}
