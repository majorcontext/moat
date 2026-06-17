package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/majorcontext/moat/internal/log"
)

const lockFileName = "daemon.lock"

// spawnLockFileName is the advisory lock file used to serialize daemon spawning.
// This prevents a race where concurrent callers all see "no daemon" and each
// spawn a new process.
const spawnLockFileName = "daemon.spawn.lock"

// DefaultProxyPort is the default port for the daemon's credential-injecting
// proxy. This is intentionally different from the routing proxy port (8080)
// to avoid conflicts on macOS where both 0.0.0.0:PORT and 127.0.0.1:PORT
// can coexist and Docker traffic hits the wrong listener.
const DefaultProxyPort = 19080

// LockInfo holds information about a running daemon.
type LockInfo struct {
	PID       int       `json:"pid"`
	ProxyPort int       `json:"proxy_port"`
	SockPath  string    `json:"sock_path"`
	StartedAt time.Time `json:"started_at"`
	Commit    string    `json:"commit,omitempty"` // Git commit hash of the daemon binary
}

// IsAlive checks if the daemon process is still running.
func (l *LockInfo) IsAlive() bool {
	process, err := os.FindProcess(l.PID)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

// WriteLockFile writes the daemon lock file.
func WriteLockFile(dir string, info LockInfo) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if info.StartedAt.IsZero() {
		info.StartedAt = time.Now()
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, lockFileName), data, 0644)
}

// ReadLockFile reads the daemon lock file. Returns nil, nil if not found.
func ReadLockFile(dir string) (*LockInfo, error) {
	data, err := os.ReadFile(filepath.Join(dir, lockFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// RemoveLockFile removes the daemon lock file.
func RemoveLockFile(dir string) {
	os.Remove(filepath.Join(dir, lockFileName))
}

// EnsureRunning checks if the daemon is already running and returns a client.
// If not running, it starts the daemon via self-exec and waits for it to be ready.
//
// An advisory file lock serializes the check-and-spawn sequence so concurrent
// callers don't each spawn a separate daemon process.
func EnsureRunning(dir string, proxyPort int) (*Client, error) {
	// Ensure the directory exists before taking the spawn lock.
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating daemon directory: %w", err)
	}

	// Acquire an advisory lock to serialize the read-check-spawn sequence.
	// Without this, concurrent callers can all see "no daemon" and each
	// spawn a new process (the root cause of the 2,854 orphaned daemons).
	unlock, err := acquireSpawnLock(dir)
	if err != nil {
		return nil, fmt.Errorf("acquiring daemon spawn lock: %w", err)
	}
	defer unlock()

	// Check existing daemon (under the lock).
	lock, err := ReadLockFile(dir)
	if err != nil {
		return nil, fmt.Errorf("reading daemon lock: %w", err)
	}

	if lock != nil && lock.IsAlive() {
		client := NewClient(lock.SockPath)
		// Verify the daemon is actually responsive (process may be alive
		// but socket deleted during partial shutdown).
		healthCtx, healthCancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, healthErr := client.Health(healthCtx)
		healthCancel()
		if healthErr == nil {
			// A healthy daemon exists. Decide whether to adopt the caller's
			// version by comparing the daemon's recorded commit against the
			// caller's BuildCommit. This is deliberately conservative: it only
			// restarts when BOTH commits are known and differ (see
			// shouldAdoptVersion). This lets a newer CLI replace a stale daemon
			// (e.g. one with an outdated MCP relay) instead of being stuck
			// behind it forever, while dev builds (commit "none"/empty) never
			// thrash because they can't be compared.
			//
			// Known limitation: if two *production* binaries with different
			// commits are installed side-by-side and both have active runs,
			// their health monitors will flip-flop restarting the daemon — each
			// EnsureRunning sees the other version and adopts its own.
			// shouldAdoptVersion only suppresses the dev-build ("none"/empty)
			// case; it does not prevent two-known-version flip-flop. We accept
			// this rather than tracking a "preferred" version, since mixed
			// production installs sharing one daemon dir are not expected.
			if shouldAdoptVersion(lock.Commit, BuildCommit) {
				log.Warn("version adoption: proxy daemon commit differs from caller; restarting daemon to adopt caller version",
					"daemon_commit", lock.Commit, "caller_commit", BuildCommit)
				// Restart under the spawn lock we already hold. restartLocked
				// requests shutdown of the existing daemon, waits for the
				// process to exit, then spawns a fresh one from this binary.
				newClient, _, restartErr := restartLocked(dir, proxyPort, lock)
				if restartErr != nil {
					// Adoption failed — keep the existing healthy daemon rather
					// than leaving callers with no proxy at all.
					log.Warn("failed to restart daemon for version adoption; keeping existing daemon",
						"error", restartErr)
					return client, nil
				}
				return newClient, nil
			}
			return client, nil
		}
		// Daemon is unresponsive — fall through to respawn.
	}

	// No healthy daemon. Clean up any stale state and spawn a fresh one.
	return spawnDaemon(dir, proxyPort, lock)
}

// spawnDaemon cleans up any stale daemon state, resolves the moat executable,
// starts a new daemon process via self-exec, and waits until it reports healthy.
// The caller MUST hold the spawn lock. The optional prev argument is the lock
// info of a daemon being replaced; its proxy port is preserved for container
// continuity and its stale lock/socket files are removed.
func spawnDaemon(dir string, proxyPort int, prev *LockInfo) (*Client, error) {
	sockPath := filepath.Join(dir, "daemon.sock")

	// Preserve proxy port from the previous daemon for container continuity.
	// Existing containers have HTTP_PROXY set to this port, so reusing
	// it avoids breaking their network after the daemon restarts.
	if proxyPort == 0 && prev != nil && prev.ProxyPort > 0 {
		proxyPort = prev.ProxyPort
	}

	// Fall back to the default daemon proxy port. Using a fixed port
	// (rather than OS-assigned 0) ensures stability across restarts.
	if proxyPort == 0 {
		proxyPort = DefaultProxyPort
	}

	// Clean up stale state.
	if prev != nil {
		RemoveLockFile(dir)
		os.Remove(prev.SockPath)
	}

	// Resolve the daemon executable.
	exe, err := resolveDaemonExecutable()
	if err != nil {
		return nil, err
	}

	args := []string{exe, "_daemon",
		"--dir", dir,
		"--proxy-port", fmt.Sprintf("%d", proxyPort),
	}

	// Open /dev/null for stdin so the daemon doesn't inherit a pipe or
	// terminal that may close when the parent exits.
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return nil, fmt.Errorf("opening /dev/null: %w", err)
	}
	defer devNull.Close()

	// Send daemon stderr to a log file for debugging startup failures.
	logPath := filepath.Join(dir, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	logFileOwned := err == nil // track whether we opened a separate file
	if !logFileOwned {
		logFile = devNull // non-fatal; discard output rather than passing nil fds
	}

	attr := &os.ProcAttr{
		Dir: "/",
		Env: os.Environ(),
		Files: []*os.File{
			devNull,
			logFile, // stdout
			logFile, // stderr
		},
		Sys: &syscall.SysProcAttr{
			Setsid: true,
		},
	}

	proc, err := os.StartProcess(exe, args, attr)
	if logFileOwned {
		logFile.Close() // daemon inherited the fd; parent can close its copy
	}
	if err != nil {
		return nil, fmt.Errorf("starting daemon: %w", err)
	}
	_ = proc.Release()

	// Wait for socket.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(sockPath); statErr == nil {
			client := NewClient(sockPath)
			pollCtx, pollCancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, healthErr := client.Health(pollCtx)
			pollCancel()
			if healthErr == nil {
				return client, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	return nil, fmt.Errorf("daemon did not start within 5 seconds")
}

// shouldAdoptVersion reports whether a healthy daemon running daemonCommit
// should be restarted so the caller's callerCommit version takes over.
//
// It is intentionally conservative: adoption happens ONLY when both commits
// are "known" (non-empty AND not the dev-build sentinel "none") and they
// differ. If either side is unknown we cannot meaningfully compare versions,
// so we keep the existing daemon to avoid restart thrashing between builds
// that all report "none".
func shouldAdoptVersion(daemonCommit, callerCommit string) bool {
	if !commitKnown(daemonCommit) || !commitKnown(callerCommit) {
		return false
	}
	return daemonCommit != callerCommit
}

// commitKnown reports whether a recorded commit string identifies a specific
// build. Empty strings and the "none" sentinel (used by plain dev builds)
// are treated as unknown.
func commitKnown(commit string) bool {
	return commit != "" && commit != "none"
}

// Restart atomically replaces the running daemon with a fresh one spawned from
// the current binary. It holds the daemon spawn lock across the entire
// stop→start sequence so concurrent EnsureRunning callers (e.g. health
// monitors) block until the new daemon is up, then observe it healthy and do
// nothing — closing the window where a stopped daemon could be resurrected by
// an old binary.
//
// The returned bool reports whether an existing daemon was stopped (true) or
// nothing was running and a fresh daemon was simply started (false).
func Restart(dir string, proxyPort int) (*Client, bool, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, false, fmt.Errorf("creating daemon directory: %w", err)
	}

	unlock, err := acquireSpawnLock(dir)
	if err != nil {
		return nil, false, fmt.Errorf("acquiring daemon spawn lock: %w", err)
	}
	defer unlock()

	lock, err := ReadLockFile(dir)
	if err != nil {
		return nil, false, fmt.Errorf("reading daemon lock: %w", err)
	}

	return restartLocked(dir, proxyPort, lock)
}

// restartLocked stops any existing daemon described by prev (waiting for the
// process to actually exit) and spawns a fresh one. The caller MUST hold the
// spawn lock. The returned bool reports whether a previous daemon was stopped.
func restartLocked(dir string, proxyPort int, prev *LockInfo) (*Client, bool, error) {
	stopped := prev != nil
	if stopped {
		// Preserve the previous daemon's proxy port for container continuity
		// before stopDaemon removes its lock file.
		if proxyPort == 0 && prev.ProxyPort > 0 {
			proxyPort = prev.ProxyPort
		}
		// stopDaemon already removes the previous lock file and socket, so pass
		// nil as prev below to avoid a redundant (and misleading) second cleanup.
		stopDaemon(dir, prev)
	}
	client, err := spawnDaemon(dir, proxyPort, nil)
	return client, stopped, err
}

// stopDaemon requests a graceful shutdown of the daemon described by lock and
// waits for the process to actually exit, escalating from a shutdown request to
// SIGTERM and finally SIGKILL. The caller MUST hold the spawn lock so no
// concurrent spawn can race the shutdown.
func stopDaemon(dir string, lock *LockInfo) {
	// Ask the daemon to shut down cleanly via its API.
	client := NewClient(lock.SockPath)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	_ = client.Shutdown(shutdownCtx)

	// Wait for the process to exit, escalating if it lingers.
	if waitForExit(lock.PID, 5*time.Second) {
		cleanupDaemonFiles(dir, lock)
		return
	}

	// Still alive — send SIGTERM.
	if proc, err := os.FindProcess(lock.PID); err == nil {
		_ = proc.Signal(syscall.SIGTERM)
	}
	if waitForExit(lock.PID, 3*time.Second) {
		cleanupDaemonFiles(dir, lock)
		return
	}

	// Last resort — SIGKILL.
	log.Warn("daemon did not exit after SIGTERM; sending SIGKILL", "pid", lock.PID)
	if proc, err := os.FindProcess(lock.PID); err == nil {
		_ = proc.Signal(syscall.SIGKILL)
	}
	if !waitForExit(lock.PID, 3*time.Second) {
		// Even SIGKILL didn't take within the window (e.g. the process is
		// stuck in uninterruptible sleep). Proceed anyway: the spawn lock is
		// held, so at worst a zombie lingers; spawning a fresh daemon is the
		// priority over blocking the caller indefinitely.
		log.Warn("daemon did not exit after SIGKILL; proceeding anyway", "pid", lock.PID)
	}
	cleanupDaemonFiles(dir, lock)
}

// cleanupDaemonFiles removes the lock file and socket left by a stopped daemon.
func cleanupDaemonFiles(dir string, lock *LockInfo) {
	RemoveLockFile(dir)
	os.Remove(lock.SockPath)
}

// processAlive reports whether the process with the given PID is still running.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// waitForExit polls until the process with the given PID is no longer alive or
// the timeout elapses. Returns true if the process exited within the timeout.
func waitForExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return !processAlive(pid)
}

// acquireSpawnLock takes an advisory file lock (flock) to serialize daemon
// spawning. Returns an unlock function that must be called (typically deferred).
func acquireSpawnLock(dir string) (unlock func(), err error) {
	lockPath := filepath.Join(dir, spawnLockFileName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}

	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// resolveDaemonExecutable determines the path to the moat binary for spawning
// the daemon process. Uses MOAT_EXECUTABLE if set, otherwise os.Executable().
// Returns an error if the resolved binary appears to be a test binary, which
// would not have the _daemon command and would produce stuck processes.
func resolveDaemonExecutable() (string, error) {
	if exe := os.Getenv("MOAT_EXECUTABLE"); exe != "" {
		return exe, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("finding executable: %w", err)
	}

	// Detect test binaries: they end in .test or have a *.test suffix
	// (e.g., "e2e.test", "daemon.test"). These don't have the _daemon
	// Cobra command and would produce stuck processes that can't parse args.
	base := filepath.Base(exe)
	if strings.HasSuffix(base, ".test") {
		return "", fmt.Errorf(
			"daemon cannot be started from test binary %q; set MOAT_EXECUTABLE to the moat binary path", exe)
	}

	return exe, nil
}
