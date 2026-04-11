package container

import (
	"context"
	"fmt"
	"io"
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

	rt, err := pool.Default()
	if err != nil {
		t.Fatalf("Default(): %v", err)
	}
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

	dflt, _ := pool.Default()
	defaultType := dflt.Type()

	rt, err := pool.Get(defaultType)
	if err != nil {
		t.Fatalf("Get(%s): %v", defaultType, err)
	}
	if rt != dflt {
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

func TestRuntimePoolCloseIdempotent(t *testing.T) {
	pool := newTestPool(t)

	if err := pool.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// --- Stub-based tests (run without a real container runtime) ---

// poolStubRuntime is a minimal Runtime implementation for pool-level tests.
// It only implements Type() and Close(); other methods panic if called.
type poolStubRuntime struct {
	closed bool
}

func (s *poolStubRuntime) Type() RuntimeType          { return RuntimeDocker }
func (s *poolStubRuntime) Close() error               { s.closed = true; return nil }
func (s *poolStubRuntime) Ping(context.Context) error { panic("not implemented") }
func (s *poolStubRuntime) CreateContainer(context.Context, Config) (string, error) {
	panic("not implemented")
}
func (s *poolStubRuntime) StartContainer(context.Context, string) error {
	panic("not implemented")
}
func (s *poolStubRuntime) StopContainer(context.Context, string) error {
	panic("not implemented")
}
func (s *poolStubRuntime) WaitContainer(context.Context, string) (int64, error) {
	panic("not implemented")
}
func (s *poolStubRuntime) RemoveContainer(context.Context, string) error {
	panic("not implemented")
}
func (s *poolStubRuntime) ContainerLogs(context.Context, string) (io.ReadCloser, error) {
	panic("not implemented")
}
func (s *poolStubRuntime) ContainerLogsAll(context.Context, string) ([]byte, error) {
	panic("not implemented")
}
func (s *poolStubRuntime) GetPortBindings(context.Context, string) (map[int]int, error) {
	panic("not implemented")
}
func (s *poolStubRuntime) GetHostAddress() string         { panic("not implemented") }
func (s *poolStubRuntime) SupportsHostNetwork() bool      { panic("not implemented") }
func (s *poolStubRuntime) NetworkManager() NetworkManager { return nil }
func (s *poolStubRuntime) SidecarManager() SidecarManager { return nil }
func (s *poolStubRuntime) BuildManager() BuildManager     { return nil }
func (s *poolStubRuntime) ServiceManager() ServiceManager { return nil }
func (s *poolStubRuntime) SetupFirewall(context.Context, string, string, int) error {
	panic("not implemented")
}
func (s *poolStubRuntime) ListImages(context.Context) ([]ImageInfo, error) {
	panic("not implemented")
}
func (s *poolStubRuntime) ListContainers(context.Context) ([]Info, error) {
	panic("not implemented")
}
func (s *poolStubRuntime) ContainerState(context.Context, string) (string, error) {
	panic("not implemented")
}
func (s *poolStubRuntime) RemoveImage(context.Context, string) error {
	panic("not implemented")
}
func (s *poolStubRuntime) StartAttached(context.Context, string, AttachOptions) error {
	panic("not implemented")
}
func (s *poolStubRuntime) ResizeTTY(context.Context, string, uint, uint) error {
	panic("not implemented")
}
func (s *poolStubRuntime) Exec(context.Context, string, []string, []byte, io.Writer, io.Writer) error {
	panic("not implemented")
}

func newStubPool() *RuntimePool {
	return NewRuntimePoolWithDefault(&poolStubRuntime{})
}

func TestRuntimePoolGetAfterClose(t *testing.T) {
	pool := newStubPool()
	pool.Close()

	// Default() should return error after Close
	_, err := pool.Default()
	if err == nil {
		t.Fatal("expected error from Default() after Close()")
	}

	// Get("") should return error after Close (legacy run path)
	_, err = pool.Get("")
	if err == nil {
		t.Fatal("expected error from Get(\"\") after Close()")
	}

	// Get(type) should return error after Close
	_, err = pool.Get(RuntimeDocker)
	if err == nil {
		t.Fatal("expected error from Get(RuntimeDocker) after Close()")
	}
}

func TestRuntimePoolGetEmptyReturnsDefault(t *testing.T) {
	pool := newStubPool()
	defer pool.Close()

	dflt, _ := pool.Default()
	rt, err := pool.Get("")
	if err != nil {
		t.Fatalf("Get(\"\"): %v", err)
	}
	if rt != dflt {
		t.Fatal("Get(\"\") should return the default runtime")
	}
}

func TestRuntimePoolForEachAvailable(t *testing.T) {
	pool := newStubPool()
	defer pool.Close()

	var visited []RuntimeType
	err := pool.ForEachAvailable(func(rt Runtime) error {
		visited = append(visited, rt.Type())
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachAvailable: %v", err)
	}

	// Should have visited at least the default runtime (stub returns RuntimeDocker)
	if len(visited) == 0 {
		t.Fatal("ForEachAvailable visited no runtimes")
	}
}

func TestRuntimePoolForEachAvailablePropagatesError(t *testing.T) {
	pool := newStubPool()
	defer pool.Close()

	testErr := fmt.Errorf("test callback error")
	err := pool.ForEachAvailable(func(rt Runtime) error {
		return testErr
	})
	if err != testErr {
		t.Fatalf("expected ForEachAvailable to propagate callback error, got: %v", err)
	}
}
