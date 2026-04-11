package container

import (
	"testing"
)

// newTestPool creates a RuntimePool for testing, skipping if no runtime is available.
func newTestPool(t *testing.T) *RuntimePool {
	t.Helper()
	pool, err := NewRuntimePool(RuntimeOptions{Sandbox: false})
	if err != nil {
		t.Skipf("no container runtime available: %v", err)
	}
	return pool
}

func TestRuntimePoolGetDefault(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()

	rt := pool.Default()
	if rt == nil {
		t.Fatal("Default() returned nil")
	}
	if rt.Type() != RuntimeDocker && rt.Type() != RuntimeApple {
		t.Fatalf("unexpected default runtime type: %s", rt.Type())
	}
}

func TestRuntimePoolGet(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()

	defaultType := pool.Default().Type()

	rt, err := pool.Get(defaultType)
	if err != nil {
		t.Fatalf("Get(%s): %v", defaultType, err)
	}
	if rt != pool.Default() {
		t.Fatal("Get(default type) returned different instance than Default()")
	}
}

func TestRuntimePoolGetUnknownType(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()

	_, err := pool.Get("unknown")
	if err == nil {
		t.Fatal("expected error for unknown runtime type")
	}
}

func TestRuntimePoolAvailable(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()

	runtimes := pool.Available()
	if len(runtimes) == 0 {
		t.Fatal("Available() returned empty list")
	}

	found := false
	for _, rt := range runtimes {
		if rt.Type() == pool.Default().Type() {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("default runtime not in Available() list")
	}
}

func TestRuntimePoolCloseIdempotent(t *testing.T) {
	pool := newTestPool(t)

	if err := pool.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
