// Package container provides an abstraction over container runtimes.
// This file implements SandboxRuntime using OS-level primitives
// (sandbox-exec on macOS, bubblewrap on Linux) instead of containers.
package container

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"text/template"
)

const (
	RuntimeSandbox RuntimeType = "sandbox"
)

// SandboxRuntime implements Runtime using OS-level sandboxing primitives.
// On macOS, it uses sandbox-exec with dynamically generated Seatbelt profiles.
// On Linux, it uses bubblewrap for namespace isolation.
type SandboxRuntime struct {
	platform  string
	processes map[string]*sandboxedProcess
	mu        sync.RWMutex
}

type sandboxedProcess struct {
	cmd        *exec.Cmd
	stdout     io.ReadCloser
	stderr     io.ReadCloser
	exitCode   int64
	exited     bool
	exitErr    error
	exitCh     chan struct{}
	workingDir string
}

// NewSandboxRuntime creates a new sandbox runtime.
func NewSandboxRuntime() (*SandboxRuntime, error) {
	r := &SandboxRuntime{
		platform:  runtime.GOOS,
		processes: make(map[string]*sandboxedProcess),
	}

	// Verify required tools are available
	switch r.platform {
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			return nil, fmt.Errorf("sandbox-exec not found (requires macOS)")
		}
	case "linux":
		if _, err := exec.LookPath("bwrap"); err != nil {
			return nil, fmt.Errorf("bubblewrap (bwrap) not found: install with 'apt install bubblewrap' or equivalent")
		}
	default:
		return nil, fmt.Errorf("sandbox runtime not supported on %s", r.platform)
	}

	return r, nil
}

func (r *SandboxRuntime) Type() RuntimeType {
	return RuntimeSandbox
}

func (r *SandboxRuntime) Ping(ctx context.Context) error {
	// Just verify the sandbox tool exists
	switch r.platform {
	case "darwin":
		return exec.CommandContext(ctx, "sandbox-exec", "-n", "no-network", "true").Run()
	case "linux":
		return exec.CommandContext(ctx, "bwrap", "--version").Run()
	}
	return nil
}

// CreateContainer creates a sandboxed process configuration (doesn't start it yet).
// For sandbox runtime, "container ID" is just a process tracking ID.
func (r *SandboxRuntime) CreateContainer(ctx context.Context, cfg Config) (string, error) {
	// Generate a unique ID for this sandbox
	id := cfg.Name
	if id == "" {
		id = generateSandboxID()
	}

	// Build the sandboxed command based on platform
	var cmd *exec.Cmd
	var err error

	switch r.platform {
	case "darwin":
		cmd, err = r.buildDarwinCommand(ctx, cfg)
	case "linux":
		cmd, err = r.buildLinuxCommand(ctx, cfg)
	default:
		return "", fmt.Errorf("unsupported platform: %s", r.platform)
	}

	if err != nil {
		return "", err
	}

	// Set up environment
	cmd.Env = append(os.Environ(), cfg.Env...)

	// Create pipes for stdout/stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("creating stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("creating stderr pipe: %w", err)
	}

	proc := &sandboxedProcess{
		cmd:        cmd,
		stdout:     stdout,
		stderr:     stderr,
		exitCh:     make(chan struct{}),
		workingDir: cfg.WorkingDir,
	}

	r.mu.Lock()
	r.processes[id] = proc
	r.mu.Unlock()

	return id, nil
}

// buildDarwinCommand creates a sandbox-exec command for macOS.
func (r *SandboxRuntime) buildDarwinCommand(ctx context.Context, cfg Config) (*exec.Cmd, error) {
	// Generate Seatbelt profile
	profile, err := r.generateSeatbeltProfile(cfg)
	if err != nil {
		return nil, fmt.Errorf("generating seatbelt profile: %w", err)
	}

	// Write profile to temp file
	profilePath := filepath.Join(os.TempDir(), fmt.Sprintf("moat-sandbox-%s.sb", cfg.Name))
	if err := os.WriteFile(profilePath, []byte(profile), 0600); err != nil {
		return nil, fmt.Errorf("writing seatbelt profile: %w", err)
	}

	// Build command: sandbox-exec -f <profile> <cmd...>
	args := []string{"-f", profilePath}

	// The actual command to run
	if len(cfg.Cmd) > 0 {
		args = append(args, cfg.Cmd...)
	} else {
		args = append(args, "/bin/bash")
	}

	cmd := exec.CommandContext(ctx, "sandbox-exec", args...)
	cmd.Dir = cfg.WorkingDir

	return cmd, nil
}

// generateSeatbeltProfile creates a macOS sandbox profile.
func (r *SandboxRuntime) generateSeatbeltProfile(cfg Config) (string, error) {
	// Template for Seatbelt profile
	// This is a restrictive profile that:
	// - Denies network by default (proxy handles allowed connections)
	// - Allows read from most places, write only to specified mounts
	// - Allows process execution
	const profileTemplate = `
(version 1)
(deny default)

;; Allow basic process operations
(allow process-fork)
(allow process-exec)
(allow signal)

;; Allow reading from everywhere (like sandbox-runtime's model)
(allow file-read*)

;; Allow writing only to specific directories
{{range .WritablePaths}}
(allow file-write* (subpath "{{.}}"))
{{end}}

;; Allow writing to temp directories
(allow file-write* (subpath "/tmp"))
(allow file-write* (subpath "/var/tmp"))
(allow file-write* (subpath (param "TMPDIR")))

;; Network: allow outbound connections (proxy will filter)
;; We allow network because the proxy handles allowlisting
(allow network-outbound)
(allow network-inbound (local ip))

;; Allow mach services for basic operation
(allow mach-lookup)

;; Allow sysctl reads for basic system info
(allow sysctl-read)
`

	// Collect writable paths from mounts
	var writablePaths []string
	for _, m := range cfg.Mounts {
		if !m.ReadOnly {
			writablePaths = append(writablePaths, m.Target)
		}
	}
	// Always allow writing to working directory
	if cfg.WorkingDir != "" {
		writablePaths = append(writablePaths, cfg.WorkingDir)
	}

	tmpl, err := template.New("seatbelt").Parse(profileTemplate)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, struct {
		WritablePaths []string
	}{
		WritablePaths: writablePaths,
	})
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

// buildLinuxCommand creates a bubblewrap command for Linux.
// nolint:unparam // error return kept for consistency with buildDarwinCommand
func (r *SandboxRuntime) buildLinuxCommand(ctx context.Context, cfg Config) (*exec.Cmd, error) {
	args := []string{
		// Create new namespaces
		"--unshare-pid",
		"--unshare-uts",
		"--unshare-ipc",
		// Note: we don't unshare network because we need proxy access
		// Network filtering is handled by the proxy

		// Die when parent dies
		"--die-with-parent",

		// Bind mount root filesystem read-only
		"--ro-bind", "/", "/",

		// Make /tmp writable
		"--tmpfs", "/tmp",
		"--tmpfs", "/var/tmp",
	}

	// Add mount bindings
	for _, m := range cfg.Mounts {
		if m.ReadOnly {
			args = append(args, "--ro-bind", m.Source, m.Target)
		} else {
			args = append(args, "--bind", m.Source, m.Target)
		}
	}

	// Set working directory
	if cfg.WorkingDir != "" {
		args = append(args, "--chdir", cfg.WorkingDir)
	}

	// Add the command to run
	if len(cfg.Cmd) > 0 {
		args = append(args, cfg.Cmd...)
	} else {
		args = append(args, "/bin/bash")
	}

	cmd := exec.CommandContext(ctx, "bwrap", args...)
	return cmd, nil
}

func (r *SandboxRuntime) StartContainer(ctx context.Context, id string) error {
	r.mu.Lock()
	proc, ok := r.processes[id]
	r.mu.Unlock()

	if !ok {
		return fmt.Errorf("sandbox %s not found", id)
	}

	if err := proc.cmd.Start(); err != nil {
		return fmt.Errorf("starting sandboxed process: %w", err)
	}

	// Monitor process exit in background
	go func() {
		err := proc.cmd.Wait()
		r.mu.Lock()
		proc.exited = true
		proc.exitErr = err
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				proc.exitCode = int64(exitErr.ExitCode())
			} else {
				proc.exitCode = -1
			}
		}
		close(proc.exitCh)
		r.mu.Unlock()
	}()

	return nil
}

func (r *SandboxRuntime) StopContainer(ctx context.Context, id string) error {
	r.mu.Lock()
	proc, ok := r.processes[id]
	r.mu.Unlock()

	if !ok {
		return fmt.Errorf("sandbox %s not found", id)
	}

	if proc.cmd.Process == nil {
		return nil // Not started
	}

	// Send SIGTERM first, then SIGKILL if needed
	if err := proc.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		// Process might already be dead
		return nil
	}

	// Wait briefly for graceful shutdown
	select {
	case <-proc.exitCh:
		return nil
	case <-ctx.Done():
		// Force kill
		_ = proc.cmd.Process.Kill()
		return ctx.Err()
	}
}

func (r *SandboxRuntime) WaitContainer(ctx context.Context, id string) (int64, error) {
	r.mu.RLock()
	proc, ok := r.processes[id]
	r.mu.RUnlock()

	if !ok {
		return -1, fmt.Errorf("sandbox %s not found", id)
	}

	select {
	case <-proc.exitCh:
		return proc.exitCode, nil
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}

func (r *SandboxRuntime) RemoveContainer(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	proc, ok := r.processes[id]
	if !ok {
		return nil // Already removed
	}

	// Clean up any temp files (like Seatbelt profiles)
	profilePath := filepath.Join(os.TempDir(), fmt.Sprintf("moat-sandbox-%s.sb", id))
	_ = os.Remove(profilePath)

	// Close pipes
	if proc.stdout != nil {
		proc.stdout.Close()
	}
	if proc.stderr != nil {
		proc.stderr.Close()
	}

	delete(r.processes, id)
	return nil
}

func (r *SandboxRuntime) ContainerLogs(ctx context.Context, id string) (io.ReadCloser, error) {
	r.mu.RLock()
	proc, ok := r.processes[id]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("sandbox %s not found", id)
	}

	// Return combined stdout+stderr reader
	return &combinedSandboxReader{
		stdout: proc.stdout,
		stderr: proc.stderr,
	}, nil
}

func (r *SandboxRuntime) ContainerLogsAll(ctx context.Context, id string) ([]byte, error) {
	// For sandbox, logs are streamed; this returns whatever's buffered
	// In practice, the streaming approach captures everything
	return nil, nil
}

func (r *SandboxRuntime) GetHostAddress() string {
	// Process runs on host, so localhost works directly
	return "127.0.0.1"
}

func (r *SandboxRuntime) SupportsHostNetwork() bool {
	// Sandbox processes run on the host network by default
	return true
}

func (r *SandboxRuntime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, proc := range r.processes {
		if proc.cmd.Process != nil {
			_ = proc.cmd.Process.Kill()
		}
	}
	r.processes = make(map[string]*sandboxedProcess)
	return nil
}

// combinedSandboxReader combines stdout and stderr into a single reader.
type combinedSandboxReader struct {
	stdout io.ReadCloser
	stderr io.ReadCloser
	mr     io.Reader
	once   sync.Once
}

func (c *combinedSandboxReader) Read(p []byte) (int, error) {
	c.once.Do(func() {
		c.mr = io.MultiReader(c.stdout, c.stderr)
	})
	return c.mr.Read(p)
}

func (c *combinedSandboxReader) Close() error {
	var errs []error
	if err := c.stdout.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := c.stderr.Close(); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func generateSandboxID() string {
	// Simple ID generation - in production, use crypto/rand
	return "sbx-" + strconv.FormatInt(int64(os.Getpid()), 16)
}

// IsSandboxAvailable checks if sandbox primitives are available on this system.
func IsSandboxAvailable() bool {
	switch runtime.GOOS {
	case "darwin":
		_, err := exec.LookPath("sandbox-exec")
		return err == nil
	case "linux":
		_, err := exec.LookPath("bwrap")
		return err == nil
	default:
		return false
	}
}
