package run

// This file holds the run lifecycle transitions: Start, StartAttached, Stop,
// Wait, and Destroy.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/snapshot"
	"github.com/majorcontext/moat/internal/term"
	"github.com/majorcontext/moat/internal/ui"
)

// Start begins execution of a run.
func (m *Manager) Start(ctx context.Context, runID string) error {
	r, err := m.beginStart(runID)
	if err != nil {
		return err
	}
	defer m.activeOps.Done()
	defer close(r.startupDone)
	setLogContext(r)

	if err := m.defaultRuntime().StartContainer(ctx, r.ContainerID); err != nil {
		return m.abortStart(r, err)
	}

	if err := m.setupFirewall(ctx, r); err != nil {
		return m.abortStart(r, err)
	}

	m.setupPortBindings(ctx, r)

	r.SetStateWithTime(StateRunning, time.Now())

	if err := r.SaveMetadata(); err != nil {
		return m.abortStart(r, err)
	}

	// Create pre-run snapshot. Skipped in volume mode: the host staging tree is
	// not the live workspace, so a pre-run snapshot of it would be meaningless.
	if r.SnapEngine != nil && !r.DisablePreRunSnapshot && !config.IsVolumeMode(r.WorkspaceMode) {
		if _, err := r.SnapEngine.Create(snapshot.TypePreRun, ""); err != nil {
			log.Debug("failed to create pre-run snapshot", "error", err)
		}
	}

	// Start background monitor to capture logs when container exits.
	// Tracked by monitorWg so Close() waits for completion. Uses monitorCtx
	// so Close() can cancel stuck monitors (prevents deadlock on custom networks).
	m.watchExit(r)

	// Start proxy health monitor to re-register the run if the daemon restarts.
	if r.ProxyRegReq != nil || len(r.Ports) > 0 {
		m.monitorWg.Add(1)
		go func() {
			defer m.monitorWg.Done()
			// Cancel when the container exits.
			proxyCtx, proxyCancel := context.WithCancel(context.Background())
			go func() {
				<-r.exitCh
				proxyCancel()
			}()
			m.monitorProxyHealth(proxyCtx, r)
		}()
	}

	return nil
}

// StartAttached starts a run with stdin/stdout/stderr attached from the beginning.
// This is required for TUI applications (like Codex CLI) that need the terminal
// connected before the process starts to properly detect terminal capabilities.
// Unlike Start + Attach, this ensures the TTY is ready when the container command begins.
func (m *Manager) StartAttached(ctx context.Context, runID string, stdin io.Reader, stdout, stderr io.Writer, size container.TTYSize) error {
	r, err := m.beginStart(runID)
	if err != nil {
		return err
	}
	defer m.activeOps.Done()
	attachCtx, cancelAttach := context.WithCancel(ctx)
	defer cancelAttach()
	if m.ctx != nil {
		stop := context.AfterFunc(m.ctx, cancelAttach)
		defer stop()
	}
	r.stateMu.Lock()
	r.attachDrained = make(chan struct{})
	drained := r.attachDrained
	r.stateMu.Unlock()
	containerID := r.ContainerID
	setLogContext(r)

	// For interactive mode, tee output to a buffer so we can capture logs.
	// This is necessary because:
	// 1. TTY mode: output goes through PTY, not container logs
	// 2. Non-TTY interactive: we may still want to capture for tests/programmatic use
	var logBuffer bytes.Buffer
	var teeStdout, teeStderr io.Writer
	teeStdout = stdout
	teeStderr = stderr

	if r.Interactive && r.Store != nil {
		// Tee stdout and stderr to capture for logs.jsonl
		teeStdout = io.MultiWriter(stdout, &logBuffer)
		if stderr != stdout {
			teeStderr = io.MultiWriter(stderr, &logBuffer)
		} else {
			// stdout and stderr are the same writer - don't duplicate
			teeStderr = teeStdout
		}
	}

	attachOpts := container.AttachOptions{
		Stdin:         stdin,
		Stdout:        teeStdout,
		Stderr:        teeStderr,
		TTY:           r.tty,
		InitialWidth:  size.Width,
		InitialHeight: size.Height,
	}

	// Channel to receive the attach result
	attachDone := make(chan error, 1)

	go func() {
		attachDone <- m.defaultRuntime().StartAttached(attachCtx, containerID, attachOpts)
	}()

	// Startup is observable only after the runtime confirms the container is
	// running. Apple can take substantially longer than a fixed delay.
	running, attachErr, err := m.awaitAttachedStart(attachCtx, r, attachDone)
	if err != nil {
		cancelAttach()
		close(drained)
		defer close(r.startupDone)
		return m.abortStart(r, err)
	}
	if running {
		if err := m.setupFirewall(ctx, r); err != nil {
			cancelAttach()
			close(drained)
			defer close(r.startupDone)
			return m.abortStart(r, err)
		}
		m.setupPortBindings(ctx, r)
		r.SetStateWithTime(StateRunning, time.Now())
		if err := r.SaveMetadata(); err != nil {
			cancelAttach()
			close(drained)
			defer close(r.startupDone)
			return m.abortStart(r, err)
		}
	}
	// Start proxy health monitor for the duration of the attached session.
	var proxyHealthCancel context.CancelFunc
	if r.ProxyRegReq != nil || len(r.Ports) > 0 {
		var proxyCtx context.Context
		proxyCtx, proxyHealthCancel = context.WithCancel(context.Background())
		m.monitorWg.Add(1)
		go func() {
			defer m.monitorWg.Done()
			m.monitorProxyHealth(proxyCtx, r)
		}()
	}

	close(r.startupDone)

	// Wait for the attachment to complete (container exits or context canceled)
	if running {
		attachErr = <-attachDone
	}

	// Stop proxy health monitor.
	if proxyHealthCancel != nil {
		proxyHealthCancel()
	}

	// Cancellation and terminal escape actions are followed by an explicit Stop.
	callerWillStop := attachCtx.Err() != nil || term.IsEscapeError(attachErr)
	// For Apple containers in interactive mode, write captured output directly to logs.jsonl.
	// (Apple TTY output doesn't go through container runtime logs — captureLogs() returns
	// early for Apple interactive runs, so this is the only path that writes logs.)
	// Runs unconditionally: even on escape-stop the buffer holds all output up to that point.
	if r.Interactive && r.Store != nil && container.RuntimeType(r.Runtime) == container.RuntimeApple {
		// Use CompareAndSwap to ensure single write
		if r.logsCaptured.CompareAndSwap(false, true) {
			if lw, err := r.Store.LogWriter(); err == nil {
				if logBuffer.Len() > 0 {
					_, _ = lw.Write(logBuffer.Bytes())
				}
				lw.Close()
			} else {
				// Failed to create file - reset flag so captureLogs can try
				r.logsCaptured.Store(false)
			}
		}
	}

	r.stateMu.Lock()
	r.attachNeedsStop = callerWillStop
	r.stateMu.Unlock()
	close(drained)
	if !callerWillStop {
		var code int64
		if attachErr == nil && m.defaultRuntime().Type() == container.RuntimeDocker {
			waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			code, attachErr = m.defaultRuntime().WaitContainer(waitCtx, containerID)
			cancel()
			if attachErr == nil && code != 0 {
				attachErr = fmt.Errorf("exit code %d", code)
			}
		}
		if attachErr != nil {
			check, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			state, inspectErr := m.defaultRuntime().ContainerState(check, r.ContainerID)
			cancel()
			if !errors.Is(inspectErr, os.ErrNotExist) && (inspectErr != nil || !containerExited(state)) {
				return m.abortStart(r, attachErr)
			}
		}
		if err := m.finishRun(r, code, attachErr); err != nil {
			return err
		}
		if r.GetState() == StateStopped {
			return nil
		}
	}

	return attachErr
}

// Stop terminates a running run.
func (m *Manager) Stop(ctx context.Context, runID string) error {
	if err := m.beginOperation(); err != nil {
		return err
	}
	defer m.activeOps.Done()
	m.mu.Lock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}

	if r.inherited {
		m.mu.Unlock()
		return m.stopObserved(ctx, r)
	}

	startup := r.startupDone
	m.mu.Unlock()
	if startup != nil {
		select {
		case <-startup:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	state := r.GetState()
	if state != StateRunning && state != StateStarting && state != StateStopping {
		return nil
	}
	accepted, err := m.requestStop(ctx, r)
	if err != nil {
		return err
	}
	r.stateMu.Lock()
	drained := r.attachDrained
	r.stateMu.Unlock()
	if drained != nil {
		select {
		case <-drained:
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			return errors.New("timed out draining attached terminal after stop")
		}
	}
	r.stateMu.Lock()
	needsStop := r.attachNeedsStop
	r.stateMu.Unlock()
	if !accepted && startup != nil && !needsStop {
		waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return r.waitForFinalization(waitCtx)
	}
	if !accepted && needsStop {
		return m.finalizeAbandonedAttach(ctx, r)
	}
	if accepted && r.Store == nil {
		r.SetState(StateStopping)
	}
	return m.finishRun(r, 0, nil)
}

// Wait blocks until the run completes.
func (m *Manager) Wait(ctx context.Context, runID string) error {
	m.mu.RLock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	m.mu.RUnlock()

	if r.inherited {
		if err := m.beginOperation(); err != nil {
			return err
		}
		defer m.activeOps.Done()
		watch, cancel := context.WithCancel(ctx)
		defer cancel()
		if m.ctx != nil {
			stop := context.AfterFunc(m.ctx, cancel)
			defer stop()
		}
		if err := m.waitForPersistedRun(watch, r); err != nil {
			return err
		}
	} else if err := r.waitForFinalization(ctx); err != nil {
		return err
	}
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return nil
}

// Stop and Wait both wait for cleanup. Only Wait returns the guest's result.
func (r *Run) waitForFinalization(ctx context.Context) error {
	select {
	case <-r.exitCh:
		r.stateMu.Lock()
		defer r.stateMu.Unlock()
		return r.completionErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Destroy removes a run and its resources.
func (m *Manager) Destroy(ctx context.Context, runID string) error {
	if err := m.beginOperation(); err != nil {
		return err
	}
	defer m.activeOps.Done()
	m.mu.Lock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	m.mu.Unlock()

	r.finishMu.Lock()
	defer r.finishMu.Unlock()
	if r.inherited {
		lock := ownerLock(r.Store.Dir())
		if _, statErr := os.Stat(lock.Path()); statErr == nil {
			held, err := lock.TryLock()
			if err != nil {
				return err
			}
			if !held {
				return fmt.Errorf("run %s is still owned by another process; stop it first", runID)
			}
			defer func() { _ = lock.Unlock() }()
			meta, err := readRunMetadata(r)
			if err != nil {
				return err
			}
			if State(meta.State) == StateStopped || State(meta.State) == StateFailed {
				r.SetState(State(meta.State))
			} else {
				rt, err := m.runtimeForRun(r)
				if err != nil {
					return err
				}
				state, err := rt.ContainerState(ctx, r.ContainerID)
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				if err == nil && !containerExited(state) && state != "created" {
					return fmt.Errorf("cannot destroy active run %s; stop it first", runID)
				}
				r.SetState(StateStopped)
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
	}
	if state := r.GetState(); state == StateRunning || state == StateStarting || state == StateStopping {
		return fmt.Errorf("cannot destroy running run %s; stop it first", runID)
	}

	// Clean up all run resources (idempotent - may already be done by Stop/monitorContainerExit)
	m.cleanupResources(ctx, r)

	// Remove the per-run workspace volume (volume mode). Unlike cleanupResources
	// (which runs at every container exit), this is the full-teardown path: the
	// volume must persist after run-stop so the user can `moat snapshot` to
	// extract their work, and is only reclaimed here at Destroy. Removed
	// unconditionally — Destroy is a complete teardown regardless of KeepContainer.
	// Best-effort: a failure here must not block the rest of Destroy.
	if r.WorkspaceVolume != "" {
		if rt, rtErr := m.runtimeForRun(r); rtErr != nil {
			log.Debug("destroy: cannot resolve runtime, skipping volume removal", "run", r.ID, "error", rtErr)
		} else if err := rt.VolumeRemove(ctx, r.WorkspaceVolume, true); err != nil {
			log.Warn("destroy: failed to remove workspace volume", "volume", r.WorkspaceVolume, "error", err)
		}
	}

	// Check if we should stop the routing proxy (no more agents with ports)
	if m.proxyLifecycle.ShouldStop() {
		if err := m.proxyLifecycle.Stop(ctx); err != nil {
			ui.Warnf("Stopping routing proxy: %v", err)
		}
	}

	// Close audit store
	if r.AuditStore != nil {
		if err := r.AuditStore.Close(); err != nil {
			ui.Warnf("Closing audit store: %v", err)
		}
	}

	// Remove run storage directory (logs, traces, metadata)
	if r.Store != nil {
		if err := r.Store.Remove(); err != nil {
			ui.Warnf("Removing storage: %v", err)
		}
	}

	r.releaseOwner()
	m.mu.Lock()
	delete(m.runs, runID)
	m.mu.Unlock()

	return nil
}

// setLogContext configures the structured logger with run-specific fields
// so all subsequent log entries in this goroutine are correlated to the run.
func setLogContext(r *Run) {
	log.SetRunContext(log.RunContext{
		RunID:     r.ID,
		RunName:   r.Name,
		Agent:     r.Agent,
		Workspace: filepath.Base(r.Workspace),
		Image:     r.Image,
		Grants:    r.Grants,
	})
}

// setupFirewall configures iptables-based network isolation inside the
// container so that only traffic through the credential-injecting proxy is
// allowed — plus the run's RFC2217 serial listener ports, which are raw TCP
// the proxy cannot relay, scoped to the host address those listeners bind.
// network.host and base_url ports are deliberately NOT in this list: they stay
// enforced on the proxy's own path, and a rule here would grant raw egress to
// that port on any reachable host. Returns an error if firewall setup fails, since a strict
// network policy without a working firewall would leave the container
// unprotected.
func (m *Manager) setupFirewall(ctx context.Context, r *Run) error {
	if !r.FirewallEnabled || r.ProxyPort <= 0 {
		return nil
	}
	if err := m.defaultRuntime().SetupFirewall(ctx, r.ContainerID, r.ProxyHost, r.ProxyPort, r.AllowedHostPorts, r.SerialHostAddr); err != nil {
		return fmt.Errorf("firewall setup failed (required for strict network policy): %w", err)
	}
	return nil
}
