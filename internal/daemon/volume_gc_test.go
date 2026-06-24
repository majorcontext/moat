package daemon

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

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
