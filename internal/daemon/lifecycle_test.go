package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLockFile_WriteAndRead(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)

	info := LockInfo{
		PID:       12345,
		ProxyPort: 9100,
		SockPath:  "/tmp/daemon.sock",
		StartedAt: now,
	}

	if err := WriteLockFile(dir, info); err != nil {
		t.Fatalf("WriteLockFile: %v", err)
	}

	got, err := ReadLockFile(dir)
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil LockInfo")
	}
	if got.PID != 12345 {
		t.Errorf("PID: expected 12345, got %d", got.PID)
	}
	if got.ProxyPort != 9100 {
		t.Errorf("ProxyPort: expected 9100, got %d", got.ProxyPort)
	}
	if got.SockPath != "/tmp/daemon.sock" {
		t.Errorf("SockPath: expected /tmp/daemon.sock, got %s", got.SockPath)
	}
	if !got.StartedAt.Equal(now) {
		t.Errorf("StartedAt: expected %v, got %v", now, got.StartedAt)
	}
}

func TestLockFile_WriteDefaultsStartedAt(t *testing.T) {
	dir := t.TempDir()
	before := time.Now()

	info := LockInfo{
		PID:       1,
		ProxyPort: 9100,
		SockPath:  "/tmp/daemon.sock",
	}

	if err := WriteLockFile(dir, info); err != nil {
		t.Fatalf("WriteLockFile: %v", err)
	}

	got, err := ReadLockFile(dir)
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	if got.StartedAt.Before(before) {
		t.Errorf("StartedAt should be at or after %v, got %v", before, got.StartedAt)
	}
}

func TestLockFile_IsAlive(t *testing.T) {
	// Current process should be alive.
	info := &LockInfo{PID: os.Getpid()}
	if !info.IsAlive() {
		t.Error("expected current process to be alive")
	}

	// A non-existent PID should not be alive.
	// Use a very high PID that is unlikely to exist.
	info = &LockInfo{PID: 4194304}
	if info.IsAlive() {
		t.Error("expected PID 4194304 to not be alive")
	}
}

func TestLockFile_Remove(t *testing.T) {
	dir := t.TempDir()

	info := LockInfo{
		PID:       1,
		ProxyPort: 9100,
		SockPath:  "/tmp/daemon.sock",
		StartedAt: time.Now(),
	}

	if err := WriteLockFile(dir, info); err != nil {
		t.Fatalf("WriteLockFile: %v", err)
	}

	// Verify file exists.
	lockPath := filepath.Join(dir, lockFileName)
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file should exist: %v", err)
	}

	RemoveLockFile(dir)

	// Verify file is gone.
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("lock file should not exist after removal, got err: %v", err)
	}

	// ReadLockFile should return nil, nil.
	got, err := ReadLockFile(dir)
	if err != nil {
		t.Fatalf("ReadLockFile after remove: %v", err)
	}
	if got != nil {
		t.Error("expected nil after remove")
	}
}

func TestLockFile_NotFound(t *testing.T) {
	dir := t.TempDir()

	got, err := ReadLockFile(dir)
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	if got != nil {
		t.Error("expected nil for missing lock file")
	}
}

func TestLockFile_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")

	info := LockInfo{
		PID:       1,
		ProxyPort: 9100,
		SockPath:  "/tmp/daemon.sock",
		StartedAt: time.Now(),
	}

	if err := WriteLockFile(dir, info); err != nil {
		t.Fatalf("WriteLockFile should create directories: %v", err)
	}

	got, err := ReadLockFile(dir)
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil LockInfo")
	}
	if got.PID != 1 {
		t.Errorf("PID: expected 1, got %d", got.PID)
	}
}

func TestLockFile_CorruptedData(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, lockFileName)

	if err := os.WriteFile(lockPath, []byte("not valid json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadLockFile(dir)
	if err == nil {
		t.Error("expected error for corrupted lock file")
	}
}

func TestAcquireSpawnLock_Serializes(t *testing.T) {
	dir := t.TempDir()

	// First lock should succeed.
	unlock1, err := acquireSpawnLock(dir)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	// Second lock in a goroutine should block until the first is released.
	done := make(chan struct{})
	go func() {
		unlock2, err := acquireSpawnLock(dir)
		if err != nil {
			t.Errorf("second lock: %v", err)
		} else {
			unlock2()
		}
		close(done)
	}()

	// Give the goroutine time to block on flock.
	time.Sleep(50 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("second lock should have blocked")
	default:
	}

	// Release first lock, second should proceed.
	unlock1()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("second lock did not complete after first was released")
	}
}

func TestResolveDaemonExecutable_RejectsTestBinary(t *testing.T) {
	// Unset MOAT_EXECUTABLE so os.Executable() is used.
	t.Setenv("MOAT_EXECUTABLE", "")

	// os.Executable() in a test binary returns a path ending in .test.
	_, err := resolveDaemonExecutable()
	if err == nil {
		t.Fatal("expected error for test binary")
	}
	if !strings.Contains(err.Error(), "test binary") {
		t.Errorf("error should mention test binary, got: %v", err)
	}
}

func TestResolveDaemonExecutable_UsesEnvOverride(t *testing.T) {
	t.Setenv("MOAT_EXECUTABLE", "/usr/local/bin/moat")

	exe, err := resolveDaemonExecutable()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exe != "/usr/local/bin/moat" {
		t.Errorf("expected /usr/local/bin/moat, got %s", exe)
	}
}
