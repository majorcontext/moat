package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const lockFileName = "daemon.lock"

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
func EnsureRunning(dir string, proxyPort int) (*Client, error) {
	sockPath := filepath.Join(dir, "daemon.sock")

	// Check existing daemon.
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

	// Start new daemon via self-exec.
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("finding executable: %w", err)
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
		logFile = nil // non-fatal; discard output
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
