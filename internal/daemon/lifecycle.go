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
)

const lockFileName = "daemon.lock"

// spawnLockFileName is the advisory lock file used to serialize daemon spawning.
// This prevents a race where concurrent callers all see "no daemon" and each
// spawn a new process.
const spawnLockFileName = "daemon.spawn.lock"

// LockInfo holds information about a running daemon.
type LockInfo struct {
	PID       int       `json:"pid"`
	ProxyPort int       `json:"proxy_port"`
	SockPath  string    `json:"sock_path"`
	StartedAt time.Time `json:"started_at"`
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
	if err := os.MkdirAll(dir, 0755); err != nil {
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
	sockPath := filepath.Join(dir, "daemon.sock")

	// Ensure the directory exists before taking the spawn lock.
	if err := os.MkdirAll(dir, 0755); err != nil {
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
		return client, nil
	}

	// Clean up stale state.
	if lock != nil {
		RemoveLockFile(dir)
		os.Remove(lock.SockPath)
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
	if err != nil {
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
	if logFile != nil {
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
			if _, healthErr := client.Health(context.Background()); healthErr == nil {
				return client, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	return nil, fmt.Errorf("daemon did not start within 5 seconds")
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
