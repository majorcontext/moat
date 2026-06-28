package run

// This file holds the run lifecycle transitions: Start, StartAttached, Stop,
// Wait, and Destroy.

import (
	"bytes"
	"context"
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
	m.mu.Lock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	m.mu.Unlock()
	r.SetState(StateStarting)
	setLogContext(r)

	if err := m.defaultRuntime().StartContainer(ctx, r.ContainerID); err != nil {
		r.SetStateFailedAt(err.Error(), time.Now())
		return err
	}

	if err := m.setupFirewall(ctx, r); err != nil {
		return err
	}

	m.setupPortBindings(ctx, r)

	r.SetStateWithTime(StateRunning, time.Now())

	// Save state to disk
	_ = r.SaveMetadata()

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
	m.monitorWg.Add(1)
	go func() {
		defer m.monitorWg.Done()
		m.monitorContainerExit(m.monitorCtx, r)
	}()

	// Start proxy health monitor to re-register the run if the daemon restarts.
	if r.ProxyRegReq != nil {
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
func (m *Manager) StartAttached(ctx context.Context, runID string, stdin io.Reader, stdout, stderr io.Writer) error {
	m.mu.Lock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	containerID := r.ContainerID
	m.mu.Unlock()
	r.SetState(StateStarting)
	setLogContext(r)

	// Start with attachment - this ensures TTY is connected before process starts.
	// TTY mode must match how the container was created (see CreateContainer in
	// docker.go and apple.go). Both runtimes only enable TTY when os.Stdin is a
	// real terminal, so we use the same check here.
	useTTY := term.IsTerminal(os.Stdin)

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
		Stdin:  stdin,
		Stdout: teeStdout,
		Stderr: teeStderr,
		TTY:    useTTY,
	}

	// Pass initial terminal size so the container can be resized immediately
	// after starting, before the process queries terminal dimensions.
	//
	// In interactive mode the CLI reserves the bottom row for a status bar
	// (see internal/tui.Writer). Subtract 1 from the reported height so the
	// child renders in rows 1..height-1 and can't collide with the footer
	// slot. Subsequent ResizeTTY calls from the CLI use the same adjustment.
	//
	// Predicate note: this site checks r.Interactive while the CLI's
	// containerTTYHeight helper checks statusWriter != nil. They're
	// equivalent today because both are gated by term.IsTerminal(os.Stdout)
	// and exec.go only constructs a statusWriter when r.Interactive is true.
	// If a future caller invokes StartAttached for an Interactive run in a
	// non-TTY context, this branch is unreached (the outer term.IsTerminal
	// guard fails first), so the predicates stay consistent.
	if useTTY && term.IsTerminal(os.Stdout) {
		width, height := term.GetSize(os.Stdout)
		if width > 0 && height > 0 {
			if r.Interactive && height > 1 {
				height--
			}
			// #nosec G115 -- width is validated positive above
			attachOpts.InitialWidth = uint(width)
			// #nosec G115 -- height is validated positive above (and only decremented when > 1)
			attachOpts.InitialHeight = uint(height)
		}
	}

	// Channel to receive the attach result
	attachDone := make(chan error, 1)

	go func() {
		attachDone <- m.defaultRuntime().StartAttached(ctx, containerID, attachOpts)
	}()

	// Give the container a moment to start before checking state.
	// See containerStartDelay for rationale.
	time.Sleep(containerStartDelay)

	// Update state to running (the container has started)
	if r.GetState() == StateStarting {
		r.SetStateWithTime(StateRunning, time.Now())
	}

	if err := m.setupFirewall(ctx, r); err != nil {
		return err
	}

	m.setupPortBindings(ctx, r)

	_ = r.SaveMetadata()

	// Start proxy health monitor for the duration of the attached session.
	var proxyHealthCancel context.CancelFunc
	if r.ProxyRegReq != nil {
		var proxyCtx context.Context
		proxyCtx, proxyHealthCancel = context.WithCancel(context.Background())
		m.monitorWg.Add(1)
		go func() {
			defer m.monitorWg.Done()
			m.monitorProxyHealth(proxyCtx, r)
		}()
	}

	// Wait for the attachment to complete (container exits or context canceled)
	attachErr := <-attachDone

	// Stop proxy health monitor.
	if proxyHealthCancel != nil {
		proxyHealthCancel()
	}

	// Determine whether the caller will stop the container (escape-stop or context
	// cancellation). In those cases, skip state updates and log capture here — the
	// caller's Stop() and monitorContainerExit handle them after the container
	// actually exits.
	callerWillStop := ctx.Err() != nil || term.IsEscapeError(attachErr)

	if !callerWillStop {
		// Container exited on its own — update state now.
		if attachErr != nil {
			r.SetStateFailedAt(attachErr.Error(), time.Now())
		} else {
			r.SetStateWithTime(StateStopped, time.Now())
		}
	}

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

	// Capture logs after container exits (critical for audit/observability).
	// Skip when caller will stop the container — it's still running and
	// monitorContainerExit will capture complete logs after it actually exits.
	if !callerWillStop {
		m.captureLogs(r)
	}

	// Run provider stopped hooks (e.g., Claude session ID extraction).
	// Must happen after the container has exited so session files are flushed.
	if !callerWillStop {
		runProviderStoppedHooks(r)
	}
	_ = r.SaveMetadata()

	// Clean up resources (network, sidecars, temp dirs) on natural exit.
	// monitorContainerExit is not running for interactive runs, so this is
	// the only cleanup path. Idempotent via cleanupOnce.
	if !callerWillStop {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		m.cleanupResources(cleanupCtx, r)
	}

	return attachErr
}

// Stop terminates a running run.
func (m *Manager) Stop(ctx context.Context, runID string) error {
	m.mu.Lock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}

	// Check state (thread-safe)
	currentState := r.GetState()
	if currentState != StateRunning && currentState != StateStarting {
		m.mu.Unlock()
		return nil // Already stopped
	}

	r.SetState(StateStopping)
	m.mu.Unlock()

	rt, rtErr := m.runtimeForRun(r)
	if rtErr != nil {
		return fmt.Errorf("resolving runtime for run %s: %w", runID, rtErr)
	}

	// Stop the main container
	if err := rt.StopContainer(ctx, r.ContainerID); err != nil {
		ui.Warnf("%v", err)
		log.Debug("failed to stop container", "container_id", r.ContainerID, "error", err)
	}

	// Capture logs and run provider hooks (both idempotent)
	m.captureLogs(r)
	runProviderStoppedHooks(r)

	r.SetStateWithTime(StateStopped, time.Now())
	_ = r.SaveMetadata()

	// Clean up all resources
	m.cleanupResources(ctx, r)

	return nil
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

	// Wait for container to exit (signaled by monitorContainerExit) or context cancellation.
	// We don't call WaitContainer here to avoid race conditions — monitorContainerExit
	// is the only goroutine that waits on the container and will close exitCh when done.
	select {
	case <-r.exitCh:
		// Container has exited (monitorContainerExit already captured logs and updated state)
		m.captureLogs(r)

		// Get final error (thread-safe read)
		var err error
		r.stateMu.Lock()
		if r.Error != "" {
			err = fmt.Errorf("%s", r.Error)
		}
		r.stateMu.Unlock()

		// Clean up resources (usually no-op because monitorContainerExit already did it)
		m.cleanupResources(context.Background(), r)

		return err
	case <-ctx.Done():
		// Context canceled — caller is responsible for stopping the run
		return ctx.Err()
	}
}

// Destroy removes a run and its resources.
func (m *Manager) Destroy(ctx context.Context, runID string) error {
	m.mu.Lock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	m.mu.Unlock()

	if r.GetState() == StateRunning {
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

// setupPortBindings retrieves the host-side port mappings for a container's
// exposed ports and registers them as routes with both the local route table
// and the proxy daemon. Port binding lookup is retried because the container
// runtime may not have mappings ready immediately after start.
func (m *Manager) setupPortBindings(ctx context.Context, r *Run) {
	if len(r.Ports) == 0 {
		return
	}

	var bindings map[int]int
	var err error
	for i := 0; i < 5; i++ {
		bindings, err = m.defaultRuntime().GetPortBindings(ctx, r.ContainerID)
		if err != nil || len(bindings) >= len(r.Ports) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		ui.Warnf("Getting port bindings: %v", err)
		return
	}

	r.HostPorts = make(map[string]int)
	services := make(map[string]string)
	for serviceName, containerPort := range r.Ports {
		if hostPort, ok := bindings[containerPort]; ok {
			r.HostPorts[serviceName] = hostPort
			services[serviceName] = fmt.Sprintf("127.0.0.1:%d", hostPort)
		}
	}
	if len(services) > 0 {
		if err := m.routes.Add(r.Name, services); err != nil {
			ui.Warnf("Registering routes: %v", err)
		}
		// Snapshot daemonClient under lock to avoid racing with Create()
		m.mu.RLock()
		dc := m.daemonClient
		m.mu.RUnlock()
		if dc != nil {
			if err := dc.RegisterRoutes(ctx, r.Name, services); err != nil {
				log.Debug("failed to register routes via daemon", "error", err)
			}
		}
	}
}

// setupFirewall configures iptables-based network isolation inside the
// container so that only traffic through the credential-injecting proxy is
// allowed. Returns an error if firewall setup fails, since a strict network
// policy without a working firewall would leave the container unprotected.
func (m *Manager) setupFirewall(ctx context.Context, r *Run) error {
	if !r.FirewallEnabled || r.ProxyPort <= 0 {
		return nil
	}
	if err := m.defaultRuntime().SetupFirewall(ctx, r.ContainerID, r.ProxyHost, r.ProxyPort); err != nil {
		r.SetStateFailedAt(fmt.Sprintf("firewall setup failed: %v", err), time.Now())
		if stopErr := m.defaultRuntime().StopContainer(ctx, r.ContainerID); stopErr != nil {
			ui.Warnf("Failed to stop container after firewall error: %v", stopErr)
		}
		return fmt.Errorf("firewall setup failed (required for strict network policy): %w", err)
	}
	return nil
}
