package daemon

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/majorcontext/moat/internal/storage"
)

// TestLiveRunIDsFromRunDirs guards the boundary the GC depends on: the live set
// is exactly the run-directory names under the base dir (no metadata.json
// required), and only directories count. A bug returning an empty or wrong set
// would make GC treat live runs as orphans and delete their volumes.
func TestLiveRunIDsFromRunDirs(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	runsDir := storage.DefaultBaseDir()
	if err := os.MkdirAll(filepath.Join(runsDir, "run_a"), 0o755); err != nil {
		t.Fatal(err)
	}
	// In-flight run: dir exists but no metadata.json yet — must still be live.
	if err := os.MkdirAll(filepath.Join(runsDir, "run_b"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A stray file is not a run dir and must be ignored.
	if err := os.WriteFile(filepath.Join(runsDir, "not-a-run"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	live, err := liveRunIDsFromRunDirs()
	if err != nil {
		t.Fatalf("liveRunIDsFromRunDirs: %v", err)
	}
	if !live["run_a"] || !live["run_b"] {
		t.Errorf("expected run_a and run_b live, got %v", live)
	}
	if live["not-a-run"] {
		t.Error("a non-directory entry must not be treated as a live run")
	}
	if len(live) != 2 {
		t.Errorf("expected exactly 2 live runs, got %d: %v", len(live), live)
	}
}

func TestOrphanVolumes(t *testing.T) {
	all := []string{"moat-ws-run_a", "moat-ws-run_b", "moat-ws-run_c", "not-a-moat-vol"}
	live := map[string]bool{"run_b": true}
	got := orphanVolumes(all, live)
	sort.Strings(got)
	want := []string{"moat-ws-run_a", "moat-ws-run_c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestOrphanVolumesNoneWhenAllLive(t *testing.T) {
	all := []string{"moat-ws-run_a", "moat-ws-run_b"}
	live := map[string]bool{"run_a": true, "run_b": true}
	if got := orphanVolumes(all, live); len(got) != 0 {
		t.Fatalf("expected no orphans, got %v", got)
	}
}

// fakeVolumeRuntime records VolumeRemove calls and serves a fixed VolumeList.
type fakeVolumeRuntime struct {
	list    []string
	removed []string
}

func (f *fakeVolumeRuntime) VolumeList(_ context.Context, _ string) ([]string, error) {
	return f.list, nil
}

func (f *fakeVolumeRuntime) VolumeRemove(_ context.Context, name string, _ bool) error {
	f.removed = append(f.removed, name)
	return nil
}

// TestGCRetainsVolumeForLiveRunDir is the companion to the orphan-removal case:
// a volume whose run id IS in the live set (its run dir still exists, i.e. the
// run is not yet destroyed) must NOT be removed. This guards the C2 fix —
// keying off run dirs instead of the (idle-empty) registry — from regressing
// into deleting persisted, un-extracted workspace volumes.
func TestGCRetainsVolumeForLiveRunDir(t *testing.T) {
	rt := &fakeVolumeRuntime{list: []string{"moat-ws-run_live", "moat-ws-run_dead"}}
	live := map[string]bool{"run_live": true}

	gcOrphanWorkspaceVolumes(context.Background(), rt.list, live, rt)

	if want := []string{"moat-ws-run_dead"}; !reflect.DeepEqual(rt.removed, want) {
		t.Fatalf("removed %v, want %v (run_live must be retained)", rt.removed, want)
	}
}
