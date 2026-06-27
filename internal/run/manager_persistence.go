package run

// This file reconstructs in-memory run state from on-disk metadata when the
// manager starts up.

import (
	"context"
	"sync"
	"time"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/storage"
)

// persistedRunInfo holds a loaded run's metadata and store, ready for container state reconciliation.
type persistedRunInfo struct {
	runID string
	store *storage.RunStore
	meta  storage.Metadata
}

// loadPersistedRuns loads run metadata from disk and reconciles with actual container state.
// Runs whose persisted state is already "stopped" or "failed" skip live container checks.
// Remaining runs are checked in parallel with bounded concurrency.
func (m *Manager) loadPersistedRuns(ctx context.Context) error {
	baseDir := storage.DefaultBaseDir()
	runIDs, err := storage.ListRunDirs(baseDir)
	if err != nil {
		return err
	}

	// Phase 1: Load metadata from disk and classify runs.
	var needCheck []persistedRunInfo
	for _, runID := range runIDs {
		store, err := storage.NewRunStore(baseDir, runID)
		if err != nil {
			log.Debug("opening run store", "id", runID, "error", err)
			continue
		}

		meta, err := store.LoadMetadata()
		if err != nil {
			log.Debug("loading run metadata", "id", runID, "error", err)
			continue
		}

		// Skip runs with no container ID (incomplete/failed creation)
		if meta.ContainerID == "" {
			continue
		}

		// Runs already in a terminal state don't need a live container check.
		// Pass stateConfirmed=true because the owning process authoritatively
		// wrote this terminal state — it's safe to clean up stale routes.
		if meta.State == string(StateStopped) || meta.State == string(StateFailed) {
			m.registerPersistedRun(State(meta.State), true, false, meta, store, runID, nil)
			continue
		}

		needCheck = append(needCheck, persistedRunInfo{runID: runID, store: store, meta: meta})
	}

	// Phase 2: Check container states in parallel with bounded concurrency.
	if len(needCheck) > 0 {
		const maxWorkers = 10
		type checkedRun struct {
			info              persistedRunInfo
			runState          State
			stateConfirmed    bool // true when state was confirmed by a successful container check
			skipMonitor       bool // true when the runtime is unavailable (cross-runtime runs)
			serviceContainers map[string]string
		}

		results := make([]checkedRun, len(needCheck))
		sem := make(chan struct{}, maxWorkers)
		var wg sync.WaitGroup

		for i, info := range needCheck {
			wg.Add(1)
			go func(idx int, info persistedRunInfo) {
				defer wg.Done()

				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					results[idx] = checkedRun{
						info:              info,
						runState:          State(info.meta.State),
						serviceContainers: info.meta.ServiceContainers,
					}
					return
				}
				defer func() { <-sem }()

				// Look up the runtime for this run (lazy-init if needed).
				rt, rtErr := m.runtimePool.Get(container.RuntimeType(info.meta.Runtime))
				if rtErr != nil {
					log.Debug("runtime not available, preserving persisted state",
						"id", info.runID, "runtime", info.meta.Runtime, "error", rtErr)
					results[idx] = checkedRun{
						info:              info,
						runState:          State(info.meta.State),
						skipMonitor:       true,
						serviceContainers: info.meta.ServiceContainers,
					}
					return
				}

				// 5-second timeout per container check.
				callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
				defer callCancel()

				var runState State
				var confirmed bool
				containerState, csErr := rt.ContainerState(callCtx, info.meta.ContainerID)
				if csErr != nil {
					log.Debug("container state check failed, preserving persisted state", "id", info.runID, "container", info.meta.ContainerID, "error", csErr)
					// Preserve both run state and service containers from
					// persisted metadata — if the runtime is unavailable,
					// service container checks would also fail.
					// Skip monitor: spawning one would also fail (same runtime issue)
					// and incorrectly mark the run as failed.
					results[idx] = checkedRun{
						info:              info,
						runState:          State(info.meta.State),
						skipMonitor:       true,
						serviceContainers: info.meta.ServiceContainers,
					}
					return
				}

				switch containerState {
				case "running":
					confirmed = true
					runState = StateRunning
				case "exited", "dead", "stopped":
					confirmed = true
					runState = StateStopped
				case "created", "restarting":
					confirmed = true
					runState = StateCreated
				default:
					// Unknown state (e.g. "paused") — can't confirm,
					// fall back to persisted state.
					runState = State(info.meta.State)
				}

				// Filter service containers to only those that still exist.
				serviceContainers := make(map[string]string, len(info.meta.ServiceContainers))
				for svcName, id := range info.meta.ServiceContainers {
					svcCtx, svcCancel := context.WithTimeout(ctx, 5*time.Second)
					if _, scErr := rt.ContainerState(svcCtx, id); scErr == nil {
						serviceContainers[svcName] = id
					}
					svcCancel()
				}

				results[idx] = checkedRun{
					info:              info,
					runState:          runState,
					stateConfirmed:    confirmed,
					serviceContainers: serviceContainers,
				}
			}(i, info)
		}

		wg.Wait()

		for _, cr := range results {
			m.registerPersistedRun(cr.runState, cr.stateConfirmed, cr.skipMonitor, cr.info.meta, cr.info.store, cr.info.runID, cr.serviceContainers)
		}
	}

	return nil
}

// registerPersistedRun creates and registers a Run from persisted metadata.
// stateConfirmed indicates whether runState was determined by a successful container
// state check (true) or inferred from persisted state / error fallback (false).
// skipMonitor prevents spawning a background monitor goroutine (used when the
// runtime is unavailable, e.g. cross-runtime runs from a different host).
// If serviceContainers is nil, it is loaded directly from metadata (for terminal-state runs
// that skip live container checks).
func (m *Manager) registerPersistedRun(runState State, stateConfirmed bool, skipMonitor bool, meta storage.Metadata, store *storage.RunStore, runID string, serviceContainers map[string]string) {
	if serviceContainers == nil {
		serviceContainers = meta.ServiceContainers
	}

	r := &Run{
		ID:                runID,
		Name:              meta.Name,
		Workspace:         meta.Workspace,
		Grants:            meta.Grants,
		Agent:             meta.Agent,
		Image:             meta.Image,
		Runtime:           meta.Runtime,
		Ports:             meta.Ports,
		State:             runState,
		ContainerID:       meta.ContainerID,
		Store:             store,
		Interactive:       meta.Interactive,
		CreatedAt:         meta.CreatedAt,
		StartedAt:         meta.StartedAt,
		StoppedAt:         meta.StoppedAt,
		Error:             meta.Error,
		ProviderMeta:      meta.ProviderMeta,
		exitCh:            make(chan struct{}),
		ServiceContainers: serviceContainers,
		NetworkID:         meta.NetworkID,
		WorktreeBranch:    meta.WorktreeBranch,
		WorktreePath:      meta.WorktreePath,
		WorktreeRepoID:    meta.WorktreeRepoID,
		WorkspaceMode:     meta.WorkspaceMode,
		WorkspaceVolume:   meta.WorkspaceVolume,
	}

	// If container is confirmed stopped by a live check or by authoritative
	// persisted state, close exitCh so Wait() calls don't hang, and clean
	// up stale routes so the name can be reused without "moat clean".
	//
	// Only perform route/daemon cleanup when stateConfirmed is true:
	// either a successful container state check confirmed the container is
	// gone, or the owning process wrote the terminal state to disk.
	//
	// When stateConfirmed is false (container check failed, context canceled,
	// or unknown container state), routes are intentionally preserved even if
	// the container is likely stopped. This avoids corrupting routes for runs
	// that are actually still alive but temporarily unreachable. The tradeoff
	// is that routes may become stale in error cases, requiring "moat clean"
	// to reclaim names. This is preferable to the previous behavior where a
	// single failed check permanently destroyed routes for live runs.
	if stateConfirmed && (runState == StateStopped || runState == StateFailed) {
		close(r.exitCh)
		if r.Name != "" {
			if err := m.routes.Remove(r.Name); err != nil {
				log.Debug("removing stale route", "name", r.Name, "error", err)
			}
			if m.daemonClient != nil {
				if err := m.daemonClient.UnregisterRoutes(context.Background(), r.Name); err != nil {
					log.Debug("failed to unregister routes via daemon", "error", err)
				}
			}
		}
	}
	// Note: no else branch for unconfirmed terminal states. All paths that
	// reach here with stateConfirmed=false have runState=running/created
	// (persisted terminal runs are caught early with stateConfirmed=true).

	// Never write state back to disk during reconciliation.
	// The owning process is responsible for its run's on-disk state.

	m.mu.Lock()
	m.runs[runID] = r
	m.mu.Unlock()

	// For running containers, start background monitor to capture logs when they exit.
	// These inherited monitors are NOT tracked by monitorWg — they may block
	// indefinitely on long-running containers from previous CLI invocations.
	// Only monitors started via Start() are tracked so Close() doesn't hang.
	// monitorContainerExit resolves the correct runtime via runtimeForRun,
	// so it works for runs from any runtime type.
	// skipMonitor is set for cross-runtime runs where the runtime is unavailable —
	// spawning a monitor would immediately fail and corrupt the persisted state.
	if runState == StateRunning && !skipMonitor {
		// Inherited monitors from persisted runs are NOT tracked by monitorWg
		// and use context.Background() — they may block indefinitely on
		// long-running containers from previous CLI invocations.
		go m.monitorContainerExit(context.Background(), r)
	}
}
