package run

// This file holds the background monitors that watch a running container exit
// and its proxy health.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/daemon"
	"github.com/majorcontext/moat/internal/log"
)

// monitorContainerExit runs once for a container started by Manager.Start.
// It records completion, signals local waiters, and releases run resources.
//
// The ctx parameter controls the WaitContainer call. Close() cancels this
// context to unblock the monitor when the manager is shutting down, preventing
// deadlocks when WaitContainer blocks indefinitely (e.g., Docker daemon slow
// to report exit on custom networks — see #315).
func (m *Manager) monitorContainerExit(ctx context.Context, r *Run) {
	// Resolve the correct runtime for this run.
	rt, rtErr := m.runtimeForRun(r)
	if rtErr != nil {
		log.Debug("cannot resolve runtime for container monitor", "run", r.ID, "error", rtErr)
		r.notifyExit(rtErr)
		r.releaseOwner()
		return
	}

	// This is the sole waiter on the live container. The context is typically
	// monitorCtx, which Close() cancels to unblock stuck monitors.
	exitCode, err := rt.WaitContainer(ctx, r.ContainerID)
	for err != nil {
		if ctx.Err() != nil {
			r.notifyExit(ctx.Err())
			return // Close releases the owner lock after all work drains.
		}
		if state := r.GetState(); state == StateStopped || state == StateFailed {
			break
		}
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		state, inspectErr := rt.ContainerState(probe, r.ContainerID)
		cancel()
		if errors.Is(inspectErr, os.ErrNotExist) || (inspectErr == nil && containerExited(state)) {
			break
		}
		select {
		case <-ctx.Done():
			r.notifyExit(ctx.Err())
			return
		case <-r.exitCh:
			return
		case <-time.After(250 * time.Millisecond):
		}
		exitCode, err = rt.WaitContainer(ctx, r.ContainerID)
	}
	if finalErr := m.finishRun(r, exitCode, err); finalErr != nil {
		log.Warn("finalizing run", "run", r.ID, "error", finalErr)
	}
}

// monitorProxyHealth repairs endpoint routing and checks the credential proxy
// daemon. Re-registering after a daemon restart prevents HTTP 407 errors when
// its in-memory registry is lost.
func (m *Manager) monitorProxyHealth(ctx context.Context, r *Run) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		// Endpoint routing has a separate listener and registration lifecycle
		// from credential injection, and must recover even for grant-free runs.
		m.recoverRunRoutes(ctx, r)

		// Snapshot daemonClient under lock.
		m.mu.RLock()
		dc := m.daemonClient
		m.mu.RUnlock()
		if dc == nil || r.ProxyAuthToken == "" || r.ProxyRegReq == nil {
			continue
		}

		// Check daemon health.
		healthCtx, healthCancel := context.WithTimeout(ctx, 5*time.Second)
		_, healthErr := dc.Health(healthCtx)
		healthCancel()

		if healthErr != nil {
			// Daemon unreachable — try to restart it.
			log.Warn("proxy daemon unreachable, attempting restart",
				"run_id", r.ID, "error", healthErr)
			proxyDir := filepath.Join(config.GlobalConfigDir(), "proxy")
			newClient, ensureErr := daemon.EnsureRunning(proxyDir, 0)
			if ensureErr != nil {
				log.Warn("failed to restart proxy daemon",
					"run_id", r.ID, "error", ensureErr)
				continue
			}
			m.mu.Lock()
			m.daemonClient = newClient
			dc = newClient
			m.mu.Unlock()
		}

		// Verify our run is still registered by trying to update it.
		updateCtx, updateCancel := context.WithTimeout(ctx, 5*time.Second)
		updateErr := dc.UpdateRun(updateCtx, r.ProxyAuthToken, r.ContainerID)
		updateCancel()

		if errors.Is(updateErr, ErrRunNotFound) {
			// Run is not registered — re-register with the same token.
			log.Info("run not found in proxy daemon, re-registering",
				"run_id", r.ID)
			regReq := *r.ProxyRegReq
			regReq.AuthToken = r.ProxyAuthToken
			regCtx, regCancel := context.WithTimeout(ctx, 5*time.Second)
			regResp, regErr := dc.RegisterRun(regCtx, regReq)
			regCancel()
			if regErr != nil {
				log.Warn("failed to re-register run with proxy daemon",
					"run_id", r.ID, "error", regErr)
				continue
			}
			// Create gates on CapSerialDevices, but this path does not: the
			// daemon we reconnect to may be an older binary that EnsureRunning
			// deliberately kept. Such a daemon ignores the serial fields it does
			// not know, answers 201, and leaves the container's frozen
			// MOAT_SERIAL_*_URL pointing at nothing. Silence is the failure
			// mode, so say so rather than logging a successful re-register.
			if len(regReq.SerialDevices) > 0 && (regResp == nil || len(regResp.SerialAddrs) == 0) {
				// log.Warn, not ui.Warn: this fires from the background
				// health-check goroutine, and an attached agent owns the
				// terminal — an unsolicited line lands mid-frame and corrupts
				// the display. Every other message in this loop logs too.
				log.Warn("re-registered run lost its serial devices; the daemon does not support them — "+
					"run 'moat proxy restart', then restart the run",
					"run_id", r.ID, "devices", len(regReq.SerialDevices))
			}
			// Update with container ID after re-registration.
			if r.ContainerID != "" {
				updCtx, updCancel := context.WithTimeout(ctx, 5*time.Second)
				_ = dc.UpdateRun(updCtx, r.ProxyAuthToken, r.ContainerID)
				updCancel()
			}
			log.Info("run re-registered with proxy daemon",
				"run_id", r.ID)
		}
	}
}
