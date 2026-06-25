package cli

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/snapshot"
)

// TestTakeSnapshotVolumeModeGuard pins the data-loss guard: the interactive
// keyboard snapshot must NOT use the in-process SnapEngine for a volume-mode run
// (it points at the read-only host staging dir, not the Docker volume, so the
// snapshot would archive the wrong tree yet still satisfy hasExtractionSnapshot
// and unlock destroy). A bind-mode run still snapshots normally. Passing a nil
// status writer makes the flash a safe no-op, so the guard branch is observable
// purely via whether a snapshot was recorded.
func TestTakeSnapshotVolumeModeGuard(t *testing.T) {
	newEngine := func() *snapshot.Engine {
		t.Helper()
		ws := t.TempDir()
		if err := os.WriteFile(filepath.Join(ws, "file.txt"), []byte("hi"), 0o600); err != nil {
			t.Fatal(err)
		}
		eng, err := snapshot.NewEngine(ws, filepath.Join(t.TempDir(), "snapshots"), snapshot.EngineOptions{})
		if err != nil {
			t.Fatalf("NewEngine: %v", err)
		}
		return eng
	}

	var timer *time.Timer
	var mu sync.Mutex

	// Volume mode: the guard fires, Create is never reached, nothing is recorded.
	volEng := newEngine()
	takeSnapshot(&run.Run{ID: "run_vol", WorkspaceMode: "volume", SnapEngine: volEng}, nil, &mu, &timer)
	if snaps, err := volEng.List(); err != nil {
		t.Fatalf("List (volume): %v", err)
	} else if len(snaps) != 0 {
		t.Errorf("volume mode: expected no snapshot, got %d", len(snaps))
	}

	// Companion — bind mode: the keyboard snapshot still works.
	bindEng := newEngine()
	takeSnapshot(&run.Run{ID: "run_bind", WorkspaceMode: "bind", SnapEngine: bindEng}, nil, &mu, &timer)
	if snaps, err := bindEng.List(); err != nil {
		t.Fatalf("List (bind): %v", err)
	} else if len(snaps) != 1 {
		t.Errorf("bind mode: expected 1 snapshot, got %d", len(snaps))
	}
}
