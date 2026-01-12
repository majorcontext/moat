package routing

import (
	"os"
	"testing"
)

func TestProxyLock(t *testing.T) {
	dir := t.TempDir()

	// No lock initially
	info, err := LoadProxyLock(dir)
	if err != nil {
		t.Fatalf("LoadProxyLock: %v", err)
	}
	if info != nil {
		t.Error("Expected nil when no lock exists")
	}

	// Create lock
	err = SaveProxyLock(dir, ProxyLockInfo{
		PID:  12345,
		Port: 8080,
	})
	if err != nil {
		t.Fatalf("SaveProxyLock: %v", err)
	}

	// Load lock
	info, err = LoadProxyLock(dir)
	if err != nil {
		t.Fatalf("LoadProxyLock: %v", err)
	}
	if info == nil {
		t.Fatal("Expected lock info")
	}
	if info.PID != 12345 {
		t.Errorf("PID = %d, want 12345", info.PID)
	}
	if info.Port != 8080 {
		t.Errorf("Port = %d, want 8080", info.Port)
	}

	// Remove lock
	err = RemoveProxyLock(dir)
	if err != nil {
		t.Fatalf("RemoveProxyLock: %v", err)
	}

	info, _ = LoadProxyLock(dir)
	if info != nil {
		t.Error("Expected nil after remove")
	}
}

func TestProxyLockIsAlive(t *testing.T) {
	// Current process should be alive
	info := &ProxyLockInfo{PID: os.Getpid()}
	if !info.IsAlive() {
		t.Error("Current process should be alive")
	}

	// Non-existent process should not be alive
	info = &ProxyLockInfo{PID: 999999999}
	if info.IsAlive() {
		t.Error("Non-existent process should not be alive")
	}
}
