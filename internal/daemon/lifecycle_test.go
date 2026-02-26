package daemon

import (
	"os"
	"path/filepath"
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
