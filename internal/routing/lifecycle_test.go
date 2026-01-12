package routing

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestProxyLifecycle(t *testing.T) {
	dir := t.TempDir()

	// Start proxy
	lc, err := NewLifecycle(dir, 0) // 0 = random port
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}

	err = lc.EnsureRunning()
	if err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}

	port := lc.Port()
	if port == 0 {
		t.Error("Port should not be 0")
	}

	// Verify proxy is accessible (will return 404 but connection succeeds)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Errorf("Proxy not accessible: %v", err)
	} else {
		resp.Body.Close()
	}

	// Stop proxy
	err = lc.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestProxyLifecycleReuse(t *testing.T) {
	dir := t.TempDir()

	// Start first instance
	lc1, err := NewLifecycle(dir, 0)
	if err != nil {
		t.Fatalf("NewLifecycle 1: %v", err)
	}
	if err := lc1.EnsureRunning(); err != nil {
		t.Fatalf("EnsureRunning 1: %v", err)
	}
	port1 := lc1.Port()

	// Second instance should reuse
	lc2, err := NewLifecycle(dir, 0)
	if err != nil {
		t.Fatalf("NewLifecycle 2: %v", err)
	}
	err = lc2.EnsureRunning()
	if err != nil {
		t.Fatalf("Second EnsureRunning: %v", err)
	}

	if lc2.Port() != port1 {
		t.Errorf("Port = %d, want %d (reused)", lc2.Port(), port1)
	}

	// Cleanup
	lc1.Stop(context.Background())
}

func TestProxyLifecyclePortMismatch(t *testing.T) {
	dir := t.TempDir()

	// Start first instance on a specific port
	lc1, err := NewLifecycle(dir, 0)
	if err != nil {
		t.Fatalf("NewLifecycle 1: %v", err)
	}
	if err := lc1.EnsureRunning(); err != nil {
		t.Fatalf("EnsureRunning 1: %v", err)
	}
	defer lc1.Stop(context.Background())

	// Second instance with different port should fail
	lc2, err := NewLifecycle(dir, 9999)
	if err != nil {
		t.Fatalf("NewLifecycle 2: %v", err)
	}
	err = lc2.EnsureRunning()
	if err == nil {
		t.Error("Expected error for port mismatch")
	}
}

func TestProxyLifecycleShouldStop(t *testing.T) {
	dir := t.TempDir()

	lc, err := NewLifecycle(dir, 0)
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}

	// No agents registered, should stop
	if !lc.ShouldStop() {
		t.Error("ShouldStop should be true when no agents")
	}

	// Register an agent
	lc.Routes().Add("test-agent", map[string]string{"web": "127.0.0.1:3000"})

	// Now should not stop
	if lc.ShouldStop() {
		t.Error("ShouldStop should be false when agents registered")
	}
}
