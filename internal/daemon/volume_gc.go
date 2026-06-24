package daemon

import (
	"context"
	"strings"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/storage"
)

// volumeNamePrefix is the prefix moat uses for per-run workspace volumes.
// Must match run.WorkspaceVolumeName ("moat-ws-" + runID).
const volumeNamePrefix = "moat-ws-"

// volumeRuntime is the subset of container.Runtime that volume GC needs.
// Declaring it locally keeps the GC logic testable and decoupled from the
// full runtime surface.
type volumeRuntime interface {
	VolumeList(ctx context.Context, prefix string) ([]string, error)
	VolumeRemove(ctx context.Context, name string, force bool) error
}

// orphanVolumes returns the moat-ws-* volume names in `all` whose run id is not
// present in liveRunIDs. Non-moat volume names are ignored.
func orphanVolumes(all []string, liveRunIDs map[string]bool) []string {
	var orphans []string
	for _, name := range all {
		if !strings.HasPrefix(name, volumeNamePrefix) {
			continue
		}
		runID := strings.TrimPrefix(name, volumeNamePrefix)
		if !liveRunIDs[runID] {
			orphans = append(orphans, name)
		}
	}
	return orphans
}

// liveRunIDsFromRunDirs builds the set of run ids considered "still alive / not
// destroyed" from the on-disk run directories under storage.DefaultBaseDir().
//
// The run directory (`~/.moat/runs/<run-id>/`) exists from Create until Destroy,
// so it — NOT the daemon's in-memory registry — is the durable signal that a run
// is not yet destroyed. The GC fires from the idle timer, which only runs while
// the registry is EMPTY; keying off the registry would make every persisted
// volume look orphaned and wipe un-extracted user work. ListRunDirNames does not
// require metadata.json, so in-flight runs (dir made, metadata not yet written)
// are also treated as live.
//
// On error, returns (nil, err); the caller skips the GC pass rather than risk
// deleting volumes for runs it merely failed to enumerate.
func liveRunIDsFromRunDirs() (map[string]bool, error) {
	names, err := storage.ListRunDirNames(storage.DefaultBaseDir())
	if err != nil {
		return nil, err
	}
	live := make(map[string]bool, len(names))
	for name := range names {
		live[name] = true
	}
	return live, nil
}

// GCOrphanWorkspaceVolumes reclaims moat-ws-<run-id> Docker named volumes whose
// run has been destroyed (its run directory no longer exists). A volume is
// normally removed on `moat destroy`, but a crashed run or SIGKILL leaks it.
// This pass enumerates moat-ws-* volumes and removes those whose run id has no
// corresponding run directory.
//
// It is intentionally defensive: any failure (no Docker, Apple runtime, list
// error, run-dir enumeration error) logs at debug/warn and returns without
// disturbing the daemon. The caller can safely invoke it during idle cleanup.
func GCOrphanWorkspaceVolumes(ctx context.Context) {
	rt, err := container.NewRuntime()
	if err != nil {
		log.Debug("skipping volume GC: no container runtime", "err", err)
		return
	}
	// List volumes BEFORE snapshotting live run dirs. This ordering avoids a
	// TOCTOU race: a run that starts between the two reads would have its run dir
	// created first (manager.go) and its volume created after, so listing volumes
	// first guarantees any volume we see has a run dir that the live snapshot
	// below will include - it can never be seen-but-not-live and wrongly
	// reclaimed. The reverse order could delete a brand-new run's volume.
	all, err := rt.VolumeList(ctx, volumeNamePrefix)
	if err != nil {
		log.Debug("skipping volume GC", "err", err)
		return
	}
	live, err := liveRunIDsFromRunDirs()
	if err != nil {
		log.Debug("skipping volume GC: cannot enumerate run dirs", "err", err)
		return
	}
	gcOrphanWorkspaceVolumes(ctx, all, live, rt)
}

// gcOrphanWorkspaceVolumes is the testable core: given the already-listed
// workspace volume names and the set of live run ids (run dirs that still
// exist), it removes the orphans. Listing is done by the caller so the
// list-before-live ordering (see GCOrphanWorkspaceVolumes) is preserved.
func gcOrphanWorkspaceVolumes(ctx context.Context, all []string, liveRunIDs map[string]bool, rt volumeRuntime) {
	for _, name := range orphanVolumes(all, liveRunIDs) {
		if err := rt.VolumeRemove(ctx, name, true); err != nil {
			log.Warn("failed to remove orphaned workspace volume", "name", name, "err", err)
		} else {
			log.Info("removed orphaned workspace volume", "name", name)
		}
	}
}
