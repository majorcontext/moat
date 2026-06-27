package run

// This file holds post-start interaction with a running container: exec,
// clipboard, TTY resize, and log access.

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/majorcontext/moat/internal/audit"
	"github.com/majorcontext/moat/internal/container"
)

// ResizeTTY resizes the container's TTY to the given dimensions.
func (m *Manager) ResizeTTY(ctx context.Context, runID string, height, width uint) error {
	m.mu.RLock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	containerID := r.ContainerID
	m.mu.RUnlock()

	rt, rtErr := m.runtimeForRun(r)
	if rtErr != nil {
		return fmt.Errorf("resolving runtime for run %s: %w", runID, rtErr)
	}
	return rt.ResizeTTY(ctx, containerID, height, width)
}

// validXclipTargets is the set of X selection targets allowed in shell commands.
var validXclipTargets = map[string]bool{
	"UTF8_STRING": true,
	"image/png":   true,
}

// WriteClipboard writes data to the X clipboard inside a running container
// using xclip. The target parameter specifies the X selection target (e.g.,
// "UTF8_STRING" for text, "image/png" for images).
func (m *Manager) WriteClipboard(ctx context.Context, runID string, data []byte, target string) error {
	// Validate target to prevent shell injection. Only known-safe X selection
	// targets are allowed; the value is interpolated into a shell command.
	if !validXclipTargets[target] {
		return fmt.Errorf("invalid xclip target: %q", target)
	}

	// Kill any previous xclip (which serves the old X selection) before
	// setting new clipboard content. xclip reads directly from stdin via
	// -i and supports large payloads through the X11 INCR mechanism.
	// setsid ensures xclip survives exec teardown so it can continue
	// serving the selection to other X clients.
	script := fmt.Sprintf(
		`pkill -x xclip 2>/dev/null; `+
			`setsid xclip -selection clipboard -t %s -i > /dev/null 2>&1`,
		target,
	)
	cmd := []string{"sh", "-c", script}
	return m.Exec(ctx, runID, cmd, data, io.Discard, io.Discard)
}

// Exec runs a command inside a running container and streams output.
func (m *Manager) Exec(ctx context.Context, runID string, cmd []string, stdin []byte, stdout, stderr io.Writer) error {
	m.mu.RLock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	containerID := r.ContainerID
	auditStore := r.AuditStore
	state := r.GetState()
	m.mu.RUnlock()

	if state != StateRunning {
		return fmt.Errorf("run %s is not running (state: %s)", runID, state)
	}

	rt, rtErr := m.runtimeForRun(r)
	if rtErr != nil {
		return fmt.Errorf("resolving runtime for run %s: %w", runID, rtErr)
	}

	execErr := rt.Exec(ctx, containerID, cmd, stdin, stdout, stderr)

	if auditStore != nil {
		exitCode := 0
		var ee *container.ExecError
		if errors.As(execErr, &ee) {
			exitCode = ee.ExitCode
		}
		_, _ = auditStore.AppendExec(audit.ExecData{
			Command:  cmd,
			HasStdin: len(stdin) > 0,
			ExitCode: exitCode,
		})
	}

	return execErr
}

// ExecInteractive runs a command inside a running container with a PTY,
// streaming the provided opts. Used by `moat join` for interactive agents.
func (m *Manager) ExecInteractive(ctx context.Context, runID string, cmd []string, opts container.ExecOptions) error {
	m.mu.RLock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	containerID := r.ContainerID
	auditStore := r.AuditStore
	state := r.GetState()
	m.mu.RUnlock()

	if state != StateRunning {
		return fmt.Errorf("run %s is not running (state: %s)", runID, state)
	}

	rt, rtErr := m.runtimeForRun(r)
	if rtErr != nil {
		return fmt.Errorf("resolving runtime for run %s: %w", runID, rtErr)
	}

	execErr := rt.ExecInteractive(ctx, containerID, cmd, opts)

	if auditStore != nil {
		exitCode := 0
		var ee *container.ExecError
		if errors.As(execErr, &ee) {
			exitCode = ee.ExitCode
		}
		_, _ = auditStore.AppendExec(audit.ExecData{
			Command:  cmd,
			HasStdin: opts.Stdin != nil,
			ExitCode: exitCode,
		})
	}
	return execErr
}

// AttachedCount returns the number of live joined agents for a run (display-only).
func (m *Manager) AttachedCount(runID string) int {
	return attachedCount(runID)
}

// RegisterJoinedAgent records a joined agent for a run and returns its index and
// a release func. Display-only; never affects teardown.
func (m *Manager) RegisterJoinedAgent(runID string) (int, func(), error) {
	return registerJoinedAgent(runID)
}

// FollowLogs streams container logs to the provided writer.
// This is more reliable than Attach for output-only mode on already-running containers.
func (m *Manager) FollowLogs(ctx context.Context, runID string, w io.Writer) error {
	m.mu.RLock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	containerID := r.ContainerID
	m.mu.RUnlock()

	rt, rtErr := m.runtimeForRun(r)
	if rtErr != nil {
		return fmt.Errorf("resolving runtime for run %s: %w", runID, rtErr)
	}

	logs, err := rt.ContainerLogs(ctx, containerID)
	if err != nil {
		return fmt.Errorf("getting container logs: %w", err)
	}
	defer logs.Close()

	_, err = io.Copy(w, logs)
	return err
}

// RecentLogs returns the last n lines of container logs.
// Used to show recent output context for a running container.
func (m *Manager) RecentLogs(runID string, lines int) (string, error) {
	m.mu.RLock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.RUnlock()
		return "", fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	containerID := r.ContainerID
	m.mu.RUnlock()

	rt, rtErr := m.runtimeForRun(r)
	if rtErr != nil {
		return "", fmt.Errorf("resolving runtime for run %s: %w", runID, rtErr)
	}

	// Get all logs (non-following)
	allLogs, err := rt.ContainerLogsAll(context.Background(), containerID)
	if err != nil {
		return "", err
	}

	// Return last n lines
	return lastNLines(string(allLogs), lines), nil
}

// lastNLines returns the last n lines of a string.
func lastNLines(s string, n int) string {
	if n <= 0 {
		return ""
	}

	// Find line boundaries from the end
	end := len(s)
	count := 0
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\n' {
			count++
			if count == n+1 {
				return s[i+1 : end]
			}
		}
	}
	// Fewer than n lines, return all
	return s
}
