package routing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestEnsureRunningPortInUse(t *testing.T) {
	// Occupy a port so the lifecycle's bind fails with EADDRINUSE.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	lc, err := NewLifecycle(t.TempDir(), port)
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}

	err = lc.EnsureRunning()
	if err == nil {
		t.Fatal("EnsureRunning: expected error when port is in use, got nil")
	}
	// The message must be actionable: name the busy port and both ways to
	// change it (env var and config key).
	for _, want := range []string{"MOAT_PROXY_PORT", "proxy.port", fmt.Sprint(port)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
	// And it must not re-introduce the raw bind-error noise it replaces.
	if strings.Contains(err.Error(), "address already in use") {
		t.Errorf("error should not embed the raw bind error: %v", err)
	}
}

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

func TestProxyLifecycleRepeatedEnsureRetainsOwnership(t *testing.T) {
	lc, err := NewLifecycle(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := lc.EnsureRunning(); err != nil {
		t.Fatal(err)
	}
	defer lc.Stop(context.Background())
	port := lc.Port()
	for i := 0; i < 3; i++ {
		if err := lc.EnsureRunning(); err != nil {
			t.Fatal(err)
		}
		if !lc.IsOwner() || lc.Port() != port {
			t.Fatal("ensure lost ownership or changed port")
		}
	}
	if err := lc.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lc.IsOwner() {
		t.Fatal("stopped lifecycle still owns proxy")
	}
	if proxyListening(port) {
		t.Fatal("Stop did not release listener")
	}
	if err := lc.EnsureRunning(); err != nil {
		t.Fatal(err)
	}
	if !lc.IsOwner() || lc.Port() != port {
		t.Fatal("restart did not retain advertised port")
	}
}

func TestProxyLifecycleRecoversDeadListenerWithLivePID(t *testing.T) {
	lc, err := NewLifecycle(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := lc.EnsureRunning(); err != nil {
		t.Fatal(err)
	}
	defer lc.Stop(context.Background())
	port := lc.Port()
	// Simulate a listener failure without removing the lock or killing a process.
	if err := lc.server.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lc.EnsureRunning(); err != nil {
		t.Fatal(err)
	}
	if !lc.IsOwner() || lc.Port() != port || !proxyListening(port) {
		t.Fatal("failed to recover listener on advertised port")
	}
}

func TestProxyLifecycleConcurrentEnsure(t *testing.T) {
	lc, err := NewLifecycle(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lc.Stop(context.Background())
	errs := make(chan error, 8)
	for i := 0; i < cap(errs); i++ {
		go func() { errs <- lc.EnsureRunning() }()
	}
	for i := 0; i < cap(errs); i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if !lc.IsOwner() {
		t.Fatal("concurrent ensure lost ownership")
	}
}

func TestProxyLifecycleOldOwnerStopPreservesReplacement(t *testing.T) {
	dir := t.TempDir()
	old, err := NewLifecycle(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.EnsureRunning(); err != nil {
		t.Fatal(err)
	}
	if err := old.server.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	replacement, err := NewLifecycle(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.EnsureRunning(); err != nil {
		t.Fatal(err)
	}
	defer replacement.Stop(context.Background())
	if err := old.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	lock, err := LoadProxyLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if lock == nil || !lock.StartedAt.Equal(replacement.startedAt) {
		t.Fatal("old owner removed replacement lock")
	}
	if !proxyListening(replacement.Port()) {
		t.Fatal("replacement stopped")
	}
}

func TestProxyLifecycleConcurrentManagers(t *testing.T) {
	dir := t.TempDir()
	managers := make([]*Lifecycle, 8)
	for i := range managers {
		lc, err := NewLifecycle(dir, 0)
		if err != nil {
			t.Fatal(err)
		}
		managers[i] = lc
		defer lc.Stop(context.Background())
	}
	errs := make(chan error, len(managers))
	for _, lc := range managers {
		go func() { errs <- lc.EnsureRunning() }()
	}
	for range managers {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	owners := 0
	for _, lc := range managers {
		if lc.IsOwner() {
			owners++
		}
		if lc.Port() != managers[0].Port() {
			t.Fatal("managers started separate listeners")
		}
	}
	if owners != 1 {
		t.Fatalf("got %d owners, want 1", owners)
	}
}

// Test helpers probe only temporary listeners created by this package.
func proxyListening(port int) bool { return probeProxy(port) == nil }

func TestLifecycleUncertainProbePreservesLiveOwner(t *testing.T) {
	dir := t.TempDir()
	owner, err := NewLifecycle(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.EnsureRunning(); err != nil {
		t.Fatal(err)
	}
	defer owner.Stop(context.Background())
	before, err := os.ReadFile(filepath.Join(dir, "proxy.lock"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewLifecycle(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	other.probe = func(int) error { return os.ErrDeadlineExceeded }
	if err := other.EnsureRunning(); err == nil {
		t.Fatal("uncertain probe accepted")
	}
	after, err := os.ReadFile(filepath.Join(dir, "proxy.lock"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("live lock changed: %v", err)
	}
	if other.server != nil || !proxyListening(owner.Port()) {
		t.Fatal("uncertain probe replaced live listener")
	}
}

func TestLifecycleFailedLockSaveReleasesListener(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write restrictions")
	}
	dir := t.TempDir()
	lc, err := NewLifecycle(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The lifecycle lock can be opened, but proxy.lock cannot be created.
	if err := os.WriteFile(filepath.Join(dir, "lifecycle.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if err := lc.EnsureRunning(); err == nil {
		t.Fatal("expected lock save error")
	}
	if lc.server != nil || lc.IsOwner() || proxyListening(lc.Port()) {
		t.Fatal("failed save leaked a listener")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := lc.EnsureRunning(); err != nil {
		t.Fatal(err)
	}
	defer lc.Stop(context.Background())
}

// Linux can reset a connection during listener shutdown before subsequent
// connections are refused. A reset alone must never authorize takeover.
func TestLifecycleResetProbe(t *testing.T) {
	reset := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNRESET}
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	for _, tc := range []struct {
		name      string
		results   []error
		stopped   bool
		wantOwner bool
		wantErr   error
	}{
		{name: "reset then refused recovers", results: []error{reset, refused}, stopped: true, wantOwner: true},
		{name: "reset then success preserves owner", results: []error{reset, nil}},
		{name: "reset then timeout preserves lock", results: []error{reset, os.ErrDeadlineExceeded}, wantErr: os.ErrDeadlineExceeded},
		{name: "persistent resets preserve lock", results: []error{reset, reset, reset}, wantErr: syscall.ECONNRESET},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			owner, err := NewLifecycle(dir, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := owner.EnsureRunning(); err != nil {
				t.Fatal(err)
			}
			defer owner.Stop(context.Background())
			port := owner.Port()
			if tc.stopped {
				if err := owner.server.Stop(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(filepath.Join(dir, "proxy.lock"))
			if err != nil {
				t.Fatal(err)
			}
			other, err := NewLifecycle(dir, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Stop(context.Background())
			calls := 0
			other.probe = func(gotPort int) error {
				if gotPort != port {
					t.Fatalf("probed port %d, want %d", gotPort, port)
				}
				if calls >= len(tc.results) {
					t.Fatal("probe exceeded retry bound")
				}
				result := tc.results[calls]
				calls++
				return result
			}
			if err := other.EnsureRunning(); !errors.Is(err, tc.wantErr) {
				t.Fatalf("EnsureRunning error = %v, want %v", err, tc.wantErr)
			}
			if calls != len(tc.results) {
				t.Fatalf("probe calls = %d, want %d", calls, len(tc.results))
			}
			if other.IsOwner() != tc.wantOwner {
				t.Fatalf("ownership = %v, want %v", other.IsOwner(), tc.wantOwner)
			}
			if !proxyListening(port) {
				t.Fatal("expected listener on original advertised port")
			}
			if !tc.wantOwner {
				after, err := os.ReadFile(filepath.Join(dir, "proxy.lock"))
				if err != nil || string(after) != string(before) {
					t.Fatalf("existing lock changed: %v", err)
				}
				if other.server != nil {
					t.Fatal("started a replacement without confirmed refusal")
				}
			}
		})
	}
}
