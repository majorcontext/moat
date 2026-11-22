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
		store, meta, err := storage.LoadRunStore(baseDir, runID)
		if err != nil {
			log.Debug("loading run metadata", "id", runID, "error", err)
			continue
		}

		// Skip runs with no container ID (incomplete/failed creation)
		if meta.ContainerID == "" {
			continue
		}

		// Runs already in a terminal state don't need a live container check.
		if meta.State == string(StateStopped) || meta.State == string(StateFailed) {
			m.registerPersistedRun(State(meta.State), meta, store, runID, nil)
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
						serviceContainers: info.meta.ServiceContainers,
					}
					return
				}

				// 5-second timeout per container check.
				callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
				defer callCancel()

				var runState State
				containerState, csErr := rt.ContainerState(callCtx, info.meta.ContainerID)
				if csErr != nil {
					log.Debug("container state check failed, preserving persisted state", "id", info.runID, "container", info.meta.ContainerID, "error", csErr)
					// Preserve both run state and service containers from
					// persisted metadata — if the runtime is unavailable,
					// service container checks would also fail.
					results[idx] = checkedRun{
						info:              info,
						runState:          State(info.meta.State),
						serviceContainers: info.meta.ServiceContainers,
					}
					return
				}

				switch containerState {
				case "running":
					runState = StateRunning
				case "exited", "dead", "stopped":
					runState = StateStopped
				case "created", "restarting":
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
					serviceContainers: serviceContainers,
				}
			}(i, info)
		}

		wg.Wait()

		for _, cr := range results {
			m.registerPersistedRun(cr.runState, cr.info.meta, cr.info.store, cr.info.runID, cr.serviceContainers)
		}
	}

	return nil
}

// registerPersistedRun creates and registers a Run from persisted metadata.
// If serviceContainers is nil, it is loaded directly from metadata (for terminal-state runs
// that skip live container checks).
func (m *Manager) registerPersistedRun(runState State, meta storage.Metadata, store *storage.RunStore, runID string, serviceContainers map[string]string) {
	if serviceContainers == nil {
		serviceContainers = meta.ServiceContainers
	}

	r := runFromMetadata(runState, meta, store, runID, serviceContainers)
	m.mu.Lock()
	m.runs[runID] = r
	m.mu.Unlock()
}

func runFromMetadata(runState State, meta storage.Metadata, store *storage.RunStore, runID string, serviceContainers map[string]string) *Run {
	return &Run{
		inherited:           true,
		cleanupPending:      meta.CleanupPending,
		ID:                  runID,
		Name:                meta.Name,
		Workspace:           meta.Workspace,
		Grants:              meta.Grants,
		Agent:               meta.Agent,
		JoinableAgents:      meta.JoinableAgents,
		Image:               meta.Image,
		Runtime:             meta.Runtime,
		Ports:               meta.Ports,
		HostPorts:           meta.HostPorts,
		State:               runState,
		ContainerID:         meta.ContainerID,
		Store:               store,
		Interactive:         meta.Interactive,
		Clipboard:           meta.Clipboard,
		KeepContainer:       meta.KeepContainer,
		BuildkitContainerID: meta.BuildkitContainerID,
		CreatedAt:           meta.CreatedAt,
		StartedAt:           meta.StartedAt,
		StoppedAt:           meta.StoppedAt,
		Error:               meta.Error,
		ProviderMeta:        meta.ProviderMeta,
		ServiceContainers:   serviceContainers,
		NetworkID:           meta.NetworkID,
		WorktreeBranch:      meta.WorktreeBranch,
		WorktreePath:        meta.WorktreePath,
		WorktreeRepoID:      meta.WorktreeRepoID,
		WorkspaceMode:       meta.WorkspaceMode,
		WorkspaceVolume:     meta.WorkspaceVolume,
	}
}
