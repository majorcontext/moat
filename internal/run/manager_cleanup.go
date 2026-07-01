package run

// This file holds run teardown: log capture, provider stop hooks, and resource
// cleanup.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
)

// captureLogs captures container logs to logs.jsonl for audit/observability.
// This method is idempotent and safe to call multiple times - it will only
// write logs once. It creates logs.jsonl even if the container produced no
// output (important for audit trail completeness).
//
// This should be called whenever a container exits, regardless of how:
// - Normal exit (Wait)
// - Interactive exit (StartAttached)
// - Explicit stop (Stop)
// - Background monitor (monitorContainerExit)
func (m *Manager) captureLogs(r *Run) {
	if r.Store == nil {
		return
	}

	// For interactive mode, logs are captured differently by runtime:
	// - Docker: Container runtime logs work even in TTY mode, so use ContainerLogsAll
	// - Apple: TTY output doesn't go to container logs, so StartAttached uses tee
	// Only skip container logs for Apple containers in interactive mode.
	if r.Interactive && container.RuntimeType(r.Runtime) == container.RuntimeApple {
		return
	}

	// Use CompareAndSwap to ensure only one goroutine captures logs at a time.
	// We DON'T check Load() first because if a previous attempt failed to create
	// the file, we want to retry. The flag is only truly "set" after successful
	// file creation below.
	if !r.logsCaptured.CompareAndSwap(false, true) {
		// Another goroutine is currently capturing or has completed.
		// Check if file exists - if so, we're done.
		logsPath := filepath.Join(r.Store.Dir(), "logs.jsonl")
		if _, err := os.Stat(logsPath); err == nil {
			log.Debug("logs already captured, skipping", "runID", r.ID)
			return
		}
		// File doesn't exist - previous attempt must have failed.
		// Reset flag and try again (we'll race with other goroutines, that's fine).
		r.logsCaptured.Store(false)
		if !r.logsCaptured.CompareAndSwap(false, true) {
			log.Debug("another goroutine is capturing logs, skipping", "runID", r.ID)
			return
		}
	}

	// Fetch all logs from the container.
	// Use a background context with timeout since the container may already be stopped.
	logCtx, logCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer logCancel()
	rt, rtErr := m.runtimeForRun(r)
	if rtErr != nil {
		log.Warn("cannot resolve runtime for log capture", "runID", r.ID, "error", rtErr)
		return
	}
	allLogs, logErr := rt.ContainerLogsAll(logCtx, r.ContainerID)
	if logErr != nil {
		log.Warn("failed to fetch container logs - creating empty logs.jsonl for audit", "runID", r.ID, "error", logErr)
		// Still create empty logs.jsonl for audit completeness
		allLogs = []byte{}
	}

	// Write logs to storage - this creates the file even if empty
	lw, lwErr := r.Store.LogWriter()
	if lwErr != nil {
		// Failed to create log file - reset flag so another goroutine can try
		r.logsCaptured.Store(false)
		log.Warn("failed to open log writer - resetting capture flag", "runID", r.ID, "error", lwErr)
		return
	}
	defer lw.Close()
	// File is now created (O_CREATE flag in LogWriter). The flag stays true.

	if len(allLogs) > 0 {
		if _, writeErr := lw.Write(allLogs); writeErr != nil {
			log.Debug("failed to write logs", "runID", r.ID, "error", writeErr)
		}
	}

	log.Debug("logs captured successfully", "runID", r.ID, "bytes", len(allLogs))
}

// runProviderStoppedHooks iterates the run's grant providers and calls
// OnRunStopped on each that implements provider.RunStoppedHook. Returned
// metadata is merged into r.ProviderMeta.
func runProviderStoppedHooks(r *Run) {
	// Ensure hooks run exactly once — multiple call sites race
	// (monitorContainerExit goroutine vs StartAttached/Stop on main goroutine).
	if !r.providerHooksDone.CompareAndSwap(false, true) {
		return
	}

	ctx := provider.RunStoppedContext{
		Workspace: r.Workspace,
		StartedAt: r.StartedAt,
	}

	for _, grant := range r.Grants {
		grantName := strings.Split(grant, ":")[0]
		prov := provider.Get(grantName)
		if prov == nil {
			continue
		}
		hook, ok := prov.(provider.RunStoppedHook)
		if !ok {
			continue
		}
		meta := hook.OnRunStopped(ctx)
		if len(meta) == 0 {
			continue
		}
		// ProviderMeta is guarded by stateMu — SaveMetadata may read it
		// concurrently from monitorContainerExit/Stop.
		r.stateMu.Lock()
		if r.ProviderMeta == nil {
			r.ProviderMeta = make(map[string]string)
		}
		for k, v := range meta {
			r.ProviderMeta[k] = v
		}
		r.stateMu.Unlock()
	}
}

// cleanupResources tears down all resources associated with a run. It is
// idempotent — only the first call does work, subsequent calls are no-ops.
// This is safe to call from Stop, Wait, monitorContainerExit, or Destroy.
//
// It does NOT:
//   - stop the main container (caller handles that)
//   - update run state (path-specific)
//   - capture logs (has its own idempotency guard)
//   - run provider stopped hooks (has its own idempotency guard)
//   - close audit store or remove storage (Destroy-only)
func (m *Manager) cleanupResources(ctx context.Context, r *Run) {
	// Snapshot daemonClient under lock to avoid racing with Create() which
	// sets it under m.mu.Lock(). cleanupResources is called from
	// monitorContainerExit goroutines that run concurrently with Create().
	m.mu.RLock()
	dc := m.daemonClient
	m.mu.RUnlock()

	r.cleanupOnce.Do(func() {
		// Resolve runtime for this run. If unavailable, skip container cleanup
		// but still clean up proxy, SSH, routes, and temp dirs.
		rt, rtErr := m.runtimeForRun(r)
		if rtErr != nil {
			log.Debug("cleanup: cannot resolve runtime, skipping container cleanup", "run", r.ID, "error", rtErr)
		}

		// Cancel token refresh and unregister run from proxy daemon
		if err := r.stopProxyServer(ctx); err != nil {
			log.Debug("cleanup: stopping proxy", "error", err)
		}
		if r.ProxyAuthToken != "" && dc != nil {
			if err := dc.UnregisterRun(ctx, r.ProxyAuthToken); err != nil {
				log.Debug("cleanup: unregistering from proxy daemon", "error", err)
			}
		}

		// Stop the SSH agent server
		if err := r.stopSSHAgentServer(); err != nil {
			log.Debug("cleanup: stopping SSH agent", "error", err)
		}

		// Stop service containers
		if rt != nil && len(r.ServiceContainers) > 0 {
			svcMgr := rt.ServiceManager()
			if svcMgr != nil {
				for svcName, svcContainerID := range r.ServiceContainers {
					log.Debug("cleanup: stopping service", "service", svcName, "container_id", svcContainerID)
					if err := svcMgr.StopService(ctx, container.ServiceInfo{ID: svcContainerID}); err != nil {
						log.Debug("cleanup: failed to stop service", "service", svcName, "error", err)
					}
				}
			}
		}

		// Remove BuildKit sidecar before network (Docker requires containers
		// disconnected before network removal, see #131).
		if rt != nil && r.BuildkitContainerID != "" {
			log.Debug("cleanup: removing buildkit sidecar", "container_id", r.BuildkitContainerID)
			if err := rt.StopContainer(ctx, r.BuildkitContainerID); err != nil {
				log.Debug("cleanup: failed to stop buildkit sidecar", "error", err)
			}
			if err := rt.RemoveContainer(ctx, r.BuildkitContainerID); err != nil {
				log.Debug("cleanup: failed to remove buildkit sidecar", "error", err)
			}
		}

		// Remove main container unless --keep was specified
		if rt != nil && !r.KeepContainer {
			if err := rt.RemoveContainer(ctx, r.ContainerID); err != nil {
				log.Debug("cleanup: failed to remove container", "error", err)
			}
		}

		// NOTE: the per-run workspace volume is intentionally NOT removed here.
		// cleanupResources runs when the container exits (Stop/Wait/monitor), but
		// in volume mode the volume is the only copy of the agent's work and must
		// survive run-stop so the user can `moat snapshot` (extract) it before
		// tearing down. Volume removal happens only in Destroy (full teardown).

		// Remove network (with force-disconnect fallback)
		if rt != nil && r.NetworkID != "" {
			netMgr := rt.NetworkManager()
			if netMgr != nil {
				if err := netMgr.RemoveNetwork(ctx, r.NetworkID); err != nil {
					log.Debug("cleanup: network removal failed, trying force", "network", r.NetworkID, "error", err)
					if forceErr := netMgr.ForceRemoveNetwork(ctx, r.NetworkID); forceErr != nil {
						log.Debug("cleanup: force network removal failed", "network", r.NetworkID, "error", forceErr)
					}
				}
			}
		}

		// Unregister routes
		if r.Name != "" {
			_ = m.routes.Remove(r.Name)
			if dc != nil {
				if err := dc.UnregisterRoutes(ctx, r.Name); err != nil {
					log.Debug("cleanup: failed to unregister routes", "error", err)
				}
			}
		}

		// Clean up provider resources
		for providerName, cleanupPath := range r.ProviderCleanupPaths {
			if prov := provider.Get(providerName); prov != nil {
				prov.Cleanup(cleanupPath)
			}
		}

		// Clean up temp directories
		for _, dir := range []string{r.awsTempDir, r.ClaudeConfigTempDir, r.CodexConfigTempDir, r.GeminiConfigTempDir, r.PiConfigTempDir} {
			if dir != "" {
				if err := os.RemoveAll(dir); err != nil {
					log.Debug("cleanup: failed to remove temp dir", "path", dir, "error", err)
				}
			}
		}
	})
}
