package container

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"
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
	closed     bool
	closeCount int
}

func (s *poolStubRuntime) Type() RuntimeType          { return RuntimeDocker }
func (s *poolStubRuntime) Close() error               { s.closed = true; s.closeCount++; return nil }
func (s *poolStubRuntime) Ping(context.Context) error { panic("not implemented") }
func (s *poolStubRuntime) CreateContainer(context.Context, Config) (string, error) {
	panic("not implemented")
}

func (s *poolStubRuntime) StartContainer(context.Context, string) error {
	panic("not implemented")
}

func (s *poolStubRuntime) VolumeCreate(context.Context, string) error {
	panic("not implemented")
}

func (s *poolStubRuntime) VolumeRemove(context.Context, string, bool) error {
	panic("not implemented")
}

func (s *poolStubRuntime) VolumeList(context.Context, string) ([]string, error) {
	panic("not implemented")
}

func (s *poolStubRuntime) VolumeExport(context.Context, string, string) error {
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

func (s *poolStubRuntime) ExecInteractive(context.Context, string, []string, ExecOptions) error {
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

func TestRuntimePoolUnavailableCached(t *testing.T) {
	pool := newStubPool()
	defer pool.Close()

	// Use a fake runtime type that will always fail NewRuntimeByType.
	const fakeType RuntimeType = "nonexistent-runtime"

	// First call: NewRuntimeByType fails, populating unavailable.
	_, err1 := pool.Get(fakeType)
	if err1 == nil {
		t.Fatal("expected error for unavailable runtime")
	}

	// Verify the unavailable map was populated.
	pool.mu.Lock()
	_, cached := pool.unavailable[fakeType]
	pool.mu.Unlock()
	if !cached {
		t.Fatal("failed runtime should be cached in unavailable map")
	}

	// Second call should return from cache (different error message — no wrapped cause).
	_, err2 := pool.Get(fakeType)
	if err2 == nil {
		t.Fatal("expected error on second Get for unavailable runtime")
	}
}

// --- GetDockerAt tests ---

func TestRuntimePoolGetDockerAtEmptyHost(t *testing.T) {
	pool := newStubPool()
	defer pool.Close()

	dflt, _ := pool.Default()
	rt, err := pool.GetDockerAt(context.Background(), "")
	if err != nil {
		t.Fatalf("GetDockerAt(\"\"): %v", err)
	}
	if rt != dflt {
		t.Fatal("GetDockerAt(\"\") should return the default runtime, same as Get(RuntimeDocker)")
	}
}

func TestRuntimePoolGetDockerAtCachesPerHost(t *testing.T) {
	srv := newFakeDockerAPIServer(t, false)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing server URL: %v", err)
	}
	host := "tcp://" + u.Host

	pool := newStubPool()
	defer pool.Close()

	rt1, err := pool.GetDockerAt(context.Background(), host)
	if err != nil {
		t.Fatalf("GetDockerAt(%q): %v", host, err)
	}
	if rt1.Type() != RuntimeDocker {
		t.Fatalf("Type() = %v, want %v", rt1.Type(), RuntimeDocker)
	}

	rt2, err := pool.GetDockerAt(context.Background(), host)
	if err != nil {
		t.Fatalf("second GetDockerAt(%q): %v", host, err)
	}
	if rt1 != rt2 {
		t.Fatal("GetDockerAt should return the same cached instance for the same host")
	}
}

func TestRuntimePoolGetDockerAtUnreachable(t *testing.T) {
	pool := newStubPool()
	defer pool.Close()

	// Port 1 is reserved and nothing should be listening there.
	_, err := pool.GetDockerAt(context.Background(), "tcp://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected error for an unreachable docker host")
	}
}

func TestRuntimePoolGetDockerAtNegativeCache(t *testing.T) {
	// A black-hole listener that accepts but never responds, so the first
	// (short-deadline) call is forced to fail via ping timeout rather than an
	// instant connection-refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn
		}
	}()
	host := "tcp://" + ln.Addr().String()

	pool := newStubPool()
	defer pool.Close()

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer shortCancel()
	_, err1 := pool.GetDockerAt(shortCtx, host)
	if err1 == nil {
		t.Fatal("expected error for an unreachable docker host")
	}

	// Second call, with a ctx that would happily wait out the full ping
	// timeout, should instead hit the negative cache and fail immediately —
	// proving the failure was cached rather than a new ping attempted.
	longCtx, longCancel := context.WithTimeout(context.Background(), dockerAtPingTimeout)
	defer longCancel()
	start := time.Now()
	_, err2 := pool.GetDockerAt(longCtx, host)
	elapsed := time.Since(start)
	if err2 == nil {
		t.Fatal("expected cached error for an unreachable docker host")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("second GetDockerAt took %s; expected a fast-fail from the negative cache instead of re-pinging", elapsed)
	}
}

func TestRuntimePoolGetDockerAtCtxCancellationAbortsPing(t *testing.T) {
	// A listener that accepts connections but never responds, so the ping
	// would otherwise block for the full dockerAtPingTimeout.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Accept and hold the connection open without responding.
			_ = conn
		}
	}()

	pool := newStubPool()
	defer pool.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err = pool.GetDockerAt(ctx, "tcp://"+ln.Addr().String())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error from GetDockerAt against a black-hole listener")
	}
	if elapsed >= dockerAtPingTimeout {
		t.Errorf("GetDockerAt took %s, expected ctx cancellation to abort the ping well before the %s ping timeout", elapsed, dockerAtPingTimeout)
	}
}

func TestRuntimePoolGetDockerAtDoesNotBlockOtherCallsDuringPing(t *testing.T) {
	// A listener that accepts connections but never responds, simulating a
	// wedged endpoint whose ping hangs for the full ping timeout.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn // accept and never respond
		}
	}()

	pool := newStubPool()
	defer pool.Close()

	host := "tcp://" + ln.Addr().String()

	// Start a GetDockerAt that will block (against its own ctx deadline) for
	// up to dockerAtPingTimeout while pinging the black-hole listener.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), dockerAtPingTimeout)
		defer cancel()
		_, _ = pool.GetDockerAt(ctx, host)
	}()

	// Give the goroutine above time to enter the ping (outside the pool
	// mutex). A concurrent Default() call must return promptly rather than
	// blocking on the wedged ping.
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	if _, err := pool.Default(); err != nil {
		t.Fatalf("Default(): %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("Default() took %s while a concurrent GetDockerAt was pinging a black-hole listener; the pool mutex should not be held across the ping", elapsed)
	}
}

func TestRuntimePoolGetDockerAtAfterClose(t *testing.T) {
	srv := newFakeDockerAPIServer(t, false)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing server URL: %v", err)
	}
	host := "tcp://" + u.Host

	pool := newStubPool()
	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := pool.GetDockerAt(context.Background(), host); err == nil {
		t.Fatal("expected error from GetDockerAt after Close()")
	}
}

func TestRuntimePoolCloseClosesDockerHosts(t *testing.T) {
	pool := newStubPool()

	stub := &poolStubRuntime{}
	pool.dockerHosts = map[string]Runtime{"tcp://127.0.0.1:1234": stub}

	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !stub.closed {
		t.Error("Close() should close host-pinned docker runtimes")
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

// --- podman-aware unreachable-endpoint error tests ---

func TestGetDockerAtPodmanHintOnPodmanLikeHost(t *testing.T) {
	pool := newStubPool()
	defer pool.Close()

	// Path contains "podman" but nothing is listening there.
	host := "unix:///tmp/does-not-exist/podman/podman.sock"
	_, err := pool.GetDockerAt(context.Background(), host)
	if err == nil {
		t.Fatal("expected error for an unreachable podman-shaped host")
	}
	if !strings.Contains(err.Error(), "podman") {
		t.Errorf("error should mention podman, got: %v", err)
	}
	if !strings.Contains(err.Error(), "metadata.json") {
		t.Errorf("error should point at the run's recorded metadata, got: %v", err)
	}
	wantHint := "podman machine start"
	if goruntime.GOOS == "linux" {
		wantHint = "systemctl --user enable --now podman.socket"
	}
	if !strings.Contains(err.Error(), wantHint) {
		t.Errorf("error should include a platform-specific restart hint (%q), got: %v", wantHint, err)
	}
}

func TestGetDockerAtNoPodmanHintOnNonPodmanHost(t *testing.T) {
	pool := newStubPool()
	defer pool.Close()

	host := "unix:///tmp/does-not-exist/docker.sock"
	_, err := pool.GetDockerAt(context.Background(), host)
	if err == nil {
		t.Fatal("expected error for an unreachable host")
	}
	if strings.Contains(err.Error(), "podman") {
		t.Errorf("error for a non-podman-shaped host should not mention podman, got: %v", err)
	}
}

// --- ForEachAvailable host-pinned runtime tests ---

func TestForEachAvailableVisitsHostPinnedRuntime(t *testing.T) {
	srv := newFakeDockerAPIServer(t, false)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing server URL: %v", err)
	}
	host := "tcp://" + u.Host

	pool := newStubPool() // default runtime is a poolStubRuntime, Type() == RuntimeDocker
	defer pool.Close()

	dockerRT, err := NewDockerRuntimeWithHost(host, false)
	if err != nil {
		t.Fatalf("NewDockerRuntimeWithHost: %v", err)
	}
	pool.dockerHosts = map[string]Runtime{host: dockerRT}

	var visited []Runtime
	if err := pool.ForEachAvailable(func(rt Runtime) error {
		visited = append(visited, rt)
		return nil
	}); err != nil {
		t.Fatalf("ForEachAvailable: %v", err)
	}

	var sawPinned bool
	for _, rt := range visited {
		if rt == Runtime(dockerRT) {
			sawPinned = true
		}
	}
	if !sawPinned {
		t.Error("ForEachAvailable should visit host-pinned Docker runtimes from GetDockerAt")
	}
}

func TestForEachAvailableSkipsSameEndpointDuplicate(t *testing.T) {
	srv := newFakeDockerAPIServer(t, false)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing server URL: %v", err)
	}
	host := "tcp://" + u.Host

	// Default runtime and a host-pinned runtime both point at the SAME
	// endpoint (constructed independently, as would happen if a run's
	// recorded DockerHost happens to equal the process's default docker
	// endpoint via two different code paths).
	defaultRT, err := NewDockerRuntimeWithHost(host, false)
	if err != nil {
		t.Fatalf("NewDockerRuntimeWithHost (default): %v", err)
	}
	pinnedRT, err := NewDockerRuntimeWithHost(host, false)
	if err != nil {
		t.Fatalf("NewDockerRuntimeWithHost (pinned): %v", err)
	}

	pool := NewRuntimePoolWithDefault(defaultRT)
	defer pool.Close()
	pool.dockerHosts = map[string]Runtime{host: pinnedRT}

	var visited []Runtime
	if err := pool.ForEachAvailable(func(rt Runtime) error {
		visited = append(visited, rt)
		return nil
	}); err != nil {
		t.Fatalf("ForEachAvailable: %v", err)
	}

	// pinnedRT itself must never be visited (it's a same-endpoint duplicate of
	// the default runtime). Other runtime types (e.g. Apple, if available on
	// this machine) may legitimately also be visited, so this doesn't assert
	// a fixed total count.
	var sawPinned, dockerVisits int
	for _, rt := range visited {
		if rt == Runtime(pinnedRT) {
			sawPinned++
		}
		if rt.Type() == RuntimeDocker {
			dockerVisits++
		}
	}
	if sawPinned != 0 {
		t.Error("ForEachAvailable should not double-visit a host-pinned runtime whose endpoint matches the already-visited default runtime")
	}
	if dockerVisits != 1 {
		t.Errorf("expected exactly 1 Docker-typed visit (the default runtime only), got %d: %+v", dockerVisits, visited)
	}
}

// --- F2: NewRuntimePoolWithDockerHost double-insert tests ---

// TestNewRuntimePoolWithDockerHostClosesRuntimeOnce pins the Close() half of
// F2: the runtime seeded as both the default and a host-pinned entry must be
// closed exactly once, not once per map it appears in.
func TestNewRuntimePoolWithDockerHostClosesRuntimeOnce(t *testing.T) {
	stub := &poolStubRuntime{}
	pool := NewRuntimePoolWithDockerHost(stub, "tcp://127.0.0.1:1234")

	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if stub.closeCount != 1 {
		t.Errorf("Close() should close the seeded runtime exactly once, got %d closes", stub.closeCount)
	}
}

// TestNewRuntimePoolWithDockerHostForEachAvailableVisitsOnce pins the
// ForEachAvailable half of F2: the old dedupe only recognized *DockerRuntime
// via DaemonHost(), so a non-*DockerRuntime stub seeded into both maps (as
// NewRuntimePoolWithDockerHost does) was visited twice.
func TestNewRuntimePoolWithDockerHostForEachAvailableVisitsOnce(t *testing.T) {
	stub := &poolStubRuntime{}
	pool := NewRuntimePoolWithDockerHost(stub, "tcp://127.0.0.1:1234")
	defer pool.Close()

	var visits int
	if err := pool.ForEachAvailable(func(rt Runtime) error {
		if rt == Runtime(stub) {
			visits++
		}
		return nil
	}); err != nil {
		t.Fatalf("ForEachAvailable: %v", err)
	}
	if visits != 1 {
		t.Errorf("ForEachAvailable should visit the seeded runtime exactly once (once as default, not again as host-pinned), got %d", visits)
	}
}

// --- F4: dockerHostsUnavailable TTL ---

// TestRuntimePoolGetDockerAtNegativeCacheExpires proves a negative-cache
// entry is retried once dockerHostNegativeCacheTTL has elapsed, using an
// injected clock rather than sleeping: the endpoint starts unreachable (no
// socket), and only starts answering after the fake clock has been advanced,
// so a stale success would be impossible — only a genuine retry after
// expiry can produce it.
func TestRuntimePoolGetDockerAtNegativeCacheExpires(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "engine.sock")
	host := "unix://" + sockPath

	pool := newStubPool()
	defer pool.Close()

	fakeNow := time.Now()
	pool.now = func() time.Time { return fakeNow }

	// Nothing listening yet: first call fails and populates the negative
	// cache, timestamped at fakeNow.
	if _, err := pool.GetDockerAt(context.Background(), host); err == nil {
		t.Fatal("expected an error before the endpoint exists")
	}

	// Bring the endpoint up, but stay within the TTL: the cached failure
	// must still be returned, proving GetDockerAt didn't just get lucky by
	// retrying regardless of TTL.
	serveFakeDockerAPIUnixSocket(t, sockPath, false)
	fakeNow = fakeNow.Add(dockerHostNegativeCacheTTL - time.Second)
	if _, err := pool.GetDockerAt(context.Background(), host); err == nil {
		t.Fatal("expected the still-cached failure to be returned before TTL expiry")
	}

	// Past the TTL: the entry must be treated as a miss and retried,
	// succeeding now that the endpoint answers.
	fakeNow = fakeNow.Add(2 * time.Second)
	rt, err := pool.GetDockerAt(context.Background(), host)
	if err != nil {
		t.Fatalf("expected the negative-cache entry to expire and retry successfully, got: %v", err)
	}
	if rt == nil {
		t.Fatal("expected a non-nil runtime after cache expiry")
	}
}
