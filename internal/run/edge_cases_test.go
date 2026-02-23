package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/deps"
	"github.com/majorcontext/moat/internal/routing"
	"github.com/majorcontext/moat/internal/storage"
)

// flexibleRuntime extends stubRuntime with configurable behavior for
// testing error paths and edge cases. Methods can be overridden by
// setting the corresponding function fields.
type flexibleRuntime struct {
	states           map[string]string
	done             chan struct{}
	startFn          func(ctx context.Context, id string) error
	stopFn           func(ctx context.Context, id string) error
	removeFn         func(ctx context.Context, id string) error
	setupFirewallFn  func(ctx context.Context, id, host string, port int) error
	waitFn           func(ctx context.Context, id string) (int64, error)
	containerLogsFn  func(ctx context.Context, id string) (io.ReadCloser, error)
	containerLogsAll func(ctx context.Context, id string) ([]byte, error)
	runtimeType      container.RuntimeType
}

func (f *flexibleRuntime) Type() container.RuntimeType {
	if f.runtimeType != "" {
		return f.runtimeType
	}
	return container.RuntimeDocker
}
func (f *flexibleRuntime) Ping(context.Context) error { return nil }
func (f *flexibleRuntime) CreateContainer(context.Context, container.Config) (string, error) {
	return "ctr-test", nil
}
func (f *flexibleRuntime) StartContainer(ctx context.Context, id string) error {
	if f.startFn != nil {
		return f.startFn(ctx, id)
	}
	return nil
}
func (f *flexibleRuntime) StopContainer(ctx context.Context, id string) error {
	if f.stopFn != nil {
		return f.stopFn(ctx, id)
	}
	return nil
}
func (f *flexibleRuntime) WaitContainer(ctx context.Context, id string) (int64, error) {
	if f.waitFn != nil {
		return f.waitFn(ctx, id)
	}
	select {
	case <-f.done:
		return 0, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}
func (f *flexibleRuntime) RemoveContainer(ctx context.Context, id string) error {
	if f.removeFn != nil {
		return f.removeFn(ctx, id)
	}
	return nil
}
func (f *flexibleRuntime) ContainerLogs(ctx context.Context, id string) (io.ReadCloser, error) {
	if f.containerLogsFn != nil {
		return f.containerLogsFn(ctx, id)
	}
	return io.NopCloser(strings.NewReader("")), nil
}
func (f *flexibleRuntime) ContainerLogsAll(ctx context.Context, id string) ([]byte, error) {
	if f.containerLogsAll != nil {
		return f.containerLogsAll(ctx, id)
	}
	return nil, nil
}
func (f *flexibleRuntime) ContainerState(_ context.Context, id string) (string, error) {
	if f.states != nil {
		state, ok := f.states[id]
		if !ok {
			return "", fmt.Errorf("container %q not found", id)
		}
		return state, nil
	}
	return "running", nil
}
func (f *flexibleRuntime) GetPortBindings(context.Context, string) (map[int]int, error) {
	return nil, nil
}
func (f *flexibleRuntime) GetHostAddress() string                   { return "127.0.0.1" }
func (f *flexibleRuntime) SupportsHostNetwork() bool                { return true }
func (f *flexibleRuntime) NetworkManager() container.NetworkManager { return nil }
func (f *flexibleRuntime) SidecarManager() container.SidecarManager { return nil }
func (f *flexibleRuntime) BuildManager() container.BuildManager     { return nil }
func (f *flexibleRuntime) ServiceManager() container.ServiceManager { return nil }
func (f *flexibleRuntime) Close() error                             { return nil }
func (f *flexibleRuntime) SetupFirewall(ctx context.Context, id, host string, port int) error {
	if f.setupFirewallFn != nil {
		return f.setupFirewallFn(ctx, id, host, port)
	}
	return nil
}
func (f *flexibleRuntime) ListImages(context.Context) ([]container.ImageInfo, error) {
	return nil, nil
}
func (f *flexibleRuntime) ListContainers(context.Context) ([]container.Info, error) {
	return nil, nil
}
func (f *flexibleRuntime) RemoveImage(context.Context, string) error { return nil }
func (f *flexibleRuntime) Attach(context.Context, string, container.AttachOptions) error {
	return nil
}
func (f *flexibleRuntime) StartAttached(context.Context, string, container.AttachOptions) error {
	return nil
}
func (f *flexibleRuntime) ResizeTTY(context.Context, string, uint, uint) error { return nil }

// newEdgeCaseManager creates a Manager with the given runtime and a temporary
// routes directory. The returned cleanup function should be deferred.
func newEdgeCaseManager(t *testing.T, rt container.Runtime) *Manager {
	t.Helper()
	tmpDir := t.TempDir()
	routeDir := filepath.Join(tmpDir, "routes")
	routes, err := routing.NewRouteTable(routeDir)
	if err != nil {
		t.Fatal(err)
	}
	return &Manager{
		runtime: rt,
		runs:    make(map[string]*Run),
		routes:  routes,
	}
}

// --- Firewall setup failure tests ---

// TestStartFirewallFailureStopsContainer verifies that when firewall setup fails
// during Start(), the container is stopped and the error is propagated.
func TestStartFirewallFailureStopsContainer(t *testing.T) {
	firewallErr := errors.New("iptables: command not found")
	var containerStopped bool

	rt := &flexibleRuntime{
		done: make(chan struct{}),
		stopFn: func(_ context.Context, _ string) error {
			containerStopped = true
			return nil
		},
		setupFirewallFn: func(context.Context, string, string, int) error {
			return firewallErr
		},
		waitFn: func(ctx context.Context, _ string) (int64, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}

	m := newEdgeCaseManager(t, rt)

	r := &Run{
		ID:              "run_fw_fail",
		Name:            "fw-test",
		ContainerID:     "ctr-fw",
		State:           StateCreated,
		FirewallEnabled: true,
		ProxyPort:       8080,
		ProxyHost:       "127.0.0.1",
		exitCh:          make(chan struct{}),
	}

	m.mu.Lock()
	m.runs[r.ID] = r
	m.mu.Unlock()

	err := m.Start(context.Background(), r.ID, StartOptions{})
	if err == nil {
		t.Fatal("Start should fail when firewall setup fails")
	}
	if !strings.Contains(err.Error(), "firewall setup failed") {
		t.Errorf("expected firewall error, got: %v", err)
	}
	if !containerStopped {
		t.Error("container should be stopped after firewall failure")
	}
}

// TestStartFirewallFailureStopContainerAlsoFails verifies graceful handling
// when both firewall setup and container stop fail.
func TestStartFirewallFailureStopContainerAlsoFails(t *testing.T) {
	rt := &flexibleRuntime{
		done: make(chan struct{}),
		stopFn: func(_ context.Context, _ string) error {
			return errors.New("stop failed too")
		},
		setupFirewallFn: func(context.Context, string, string, int) error {
			return errors.New("iptables error")
		},
		waitFn: func(ctx context.Context, _ string) (int64, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}

	m := newEdgeCaseManager(t, rt)

	r := &Run{
		ID:              "run_double_fail",
		Name:            "double-fail",
		ContainerID:     "ctr-double",
		State:           StateCreated,
		FirewallEnabled: true,
		ProxyPort:       8080,
		ProxyHost:       "127.0.0.1",
		exitCh:          make(chan struct{}),
	}

	m.mu.Lock()
	m.runs[r.ID] = r
	m.mu.Unlock()

	err := m.Start(context.Background(), r.ID, StartOptions{})
	if err == nil {
		t.Fatal("Start should fail when firewall setup fails")
	}
	// Should still report the firewall error even though stop also failed
	if !strings.Contains(err.Error(), "firewall setup failed") {
		t.Errorf("expected firewall error, got: %v", err)
	}
}

// TestStartNoFirewallWhenNotEnabled verifies that firewall setup is skipped
// when FirewallEnabled is false, even if ProxyPort is set.
func TestStartNoFirewallWhenNotEnabled(t *testing.T) {
	firewallCalled := false
	rt := &flexibleRuntime{
		done: make(chan struct{}),
		setupFirewallFn: func(context.Context, string, string, int) error {
			firewallCalled = true
			return nil
		},
		waitFn: func(ctx context.Context, _ string) (int64, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		},
		containerLogsFn: func(ctx context.Context, _ string) (io.ReadCloser, error) {
			// Block until context canceled so streamLogsToStorage doesn't finish instantly
			<-ctx.Done()
			return io.NopCloser(strings.NewReader("")), ctx.Err()
		},
	}

	m := newEdgeCaseManager(t, rt)

	store, err := storage.NewRunStore(t.TempDir(), "run_no_fw")
	if err != nil {
		t.Fatal(err)
	}

	r := &Run{
		ID:              "run_no_fw",
		Name:            "no-fw",
		ContainerID:     "ctr-nofw",
		State:           StateCreated,
		FirewallEnabled: false,
		ProxyPort:       8080,
		Store:           store,
		exitCh:          make(chan struct{}),
	}

	m.mu.Lock()
	m.runs[r.ID] = r
	m.mu.Unlock()

	err = m.Start(context.Background(), r.ID, StartOptions{})
	if err != nil {
		t.Fatalf("Start should succeed: %v", err)
	}
	if firewallCalled {
		t.Error("firewall setup should not be called when not enabled")
	}
}

// --- Cleanup error path tests ---

// TestStopAlreadyStopped verifies that calling Stop on an already-stopped
// run is a no-op (idempotent).
func TestStopAlreadyStopped(t *testing.T) {
	rt := &flexibleRuntime{done: make(chan struct{})}
	m := newEdgeCaseManager(t, rt)

	r := &Run{
		ID:          "run_stopped",
		Name:        "already-stopped",
		ContainerID: "ctr-stopped",
		State:       StateStopped,
		exitCh:      make(chan struct{}),
	}
	close(r.exitCh)

	m.mu.Lock()
	m.runs[r.ID] = r
	m.mu.Unlock()

	err := m.Stop(context.Background(), r.ID)
	if err != nil {
		t.Fatalf("Stop on already-stopped run should not error: %v", err)
	}
	if r.GetState() != StateStopped {
		t.Errorf("state should still be stopped, got %s", r.GetState())
	}
}

// TestStopNotFound verifies Stop returns error for unknown run ID.
func TestStopNotFound(t *testing.T) {
	rt := &flexibleRuntime{done: make(chan struct{})}
	m := newEdgeCaseManager(t, rt)

	err := m.Stop(context.Background(), "run_nonexistent")
	if err == nil {
		t.Fatal("Stop should fail for non-existent run")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

// TestStopHandlesContainerStopError verifies that Stop continues cleanup
// even when StopContainer fails.
func TestStopHandlesContainerStopError(t *testing.T) {
	rt := &flexibleRuntime{
		done: make(chan struct{}),
		stopFn: func(_ context.Context, _ string) error {
			return errors.New("container already removed")
		},
	}
	m := newEdgeCaseManager(t, rt)

	r := &Run{
		ID:          "run_stop_err",
		Name:        "stop-err",
		ContainerID: "ctr-gone",
		State:       StateRunning,
		exitCh:      make(chan struct{}),
	}

	m.mu.Lock()
	m.runs[r.ID] = r
	m.mu.Unlock()

	// Stop should not return error even though StopContainer failed
	err := m.Stop(context.Background(), r.ID)
	if err != nil {
		t.Fatalf("Stop should succeed despite container stop error: %v", err)
	}
	if r.GetState() != StateStopped {
		t.Errorf("state should be stopped, got %s", r.GetState())
	}
}

// TestStopHandlesRemoveContainerError verifies that Stop completes
// even when RemoveContainer fails.
func TestStopHandlesRemoveContainerError(t *testing.T) {
	rt := &flexibleRuntime{
		done: make(chan struct{}),
		removeFn: func(_ context.Context, _ string) error {
			return errors.New("permission denied")
		},
	}
	m := newEdgeCaseManager(t, rt)

	r := &Run{
		ID:            "run_rm_err",
		Name:          "rm-err",
		ContainerID:   "ctr-perm",
		State:         StateRunning,
		KeepContainer: false,
		exitCh:        make(chan struct{}),
	}

	m.mu.Lock()
	m.runs[r.ID] = r
	m.mu.Unlock()

	err := m.Stop(context.Background(), r.ID)
	if err != nil {
		t.Fatalf("Stop should succeed despite remove error: %v", err)
	}
	if r.GetState() != StateStopped {
		t.Errorf("state should be stopped, got %s", r.GetState())
	}
}

// TestStopKeepContainer verifies that --keep prevents container removal.
func TestStopKeepContainer(t *testing.T) {
	removeAttempted := false
	rt := &flexibleRuntime{
		done: make(chan struct{}),
		removeFn: func(_ context.Context, _ string) error {
			removeAttempted = true
			return nil
		},
	}
	m := newEdgeCaseManager(t, rt)

	r := &Run{
		ID:            "run_keep",
		Name:          "keep-me",
		ContainerID:   "ctr-keep",
		State:         StateRunning,
		KeepContainer: true,
		exitCh:        make(chan struct{}),
	}

	m.mu.Lock()
	m.runs[r.ID] = r
	m.mu.Unlock()

	err := m.Stop(context.Background(), r.ID)
	if err != nil {
		t.Fatalf("Stop should succeed: %v", err)
	}
	if removeAttempted {
		t.Error("container should not be removed when KeepContainer is true")
	}
}

// --- Concurrent access verification tests ---

// TestConcurrentGetState verifies that GetState/SetState are safe
// under concurrent access (run with -race to verify).
func TestConcurrentGetState(t *testing.T) {
	r := &Run{
		ID:    "run_concurrent",
		State: StateCreated,
	}

	var wg sync.WaitGroup
	states := []State{StateStarting, StateRunning, StateStopping, StateStopped}

	// Multiple writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.SetState(states[idx%len(states)])
			}
		}(i)
	}

	// Multiple readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				state := r.GetState()
				// Just verify it's one of the valid states
				switch state {
				case StateCreated, StateStarting, StateRunning, StateStopping, StateStopped, StateFailed:
					// valid
				default:
					t.Errorf("unexpected state: %s", state)
				}
			}
		}()
	}

	wg.Wait()
}

// TestConcurrentSetStateWithError verifies that SetStateWithError is safe
// under concurrent access (run with -race to verify).
func TestConcurrentSetStateWithError(t *testing.T) {
	r := &Run{
		ID:    "run_conc_err",
		State: StateRunning,
	}

	var wg sync.WaitGroup

	// Concurrent state changes with errors
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if idx%2 == 0 {
					r.SetStateWithError(StateFailed, fmt.Sprintf("error-%d-%d", idx, j))
				} else {
					r.SetStateWithTime(StateStopped, time.Now())
				}
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = r.GetState()
			}
		}()
	}

	wg.Wait()
}

// TestConcurrentStopProxyServer verifies that stopProxyServer is safe
// to call from multiple goroutines (sync.Once guarantees).
func TestConcurrentStopProxyServer(t *testing.T) {
	r := &Run{
		ID:    "run_conc_proxy",
		State: StateRunning,
	}

	var wg sync.WaitGroup

	// Call stopProxyServer concurrently - should not panic
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.stopProxyServer(context.Background())
		}()
	}

	wg.Wait()
}

// TestConcurrentStopSSHAgent verifies that stopSSHAgentServer is safe
// to call from multiple goroutines (sync.Once guarantees).
func TestConcurrentStopSSHAgent(t *testing.T) {
	r := &Run{
		ID:    "run_conc_ssh",
		State: StateRunning,
	}

	var wg sync.WaitGroup

	// Call stopSSHAgentServer concurrently - should not panic
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.stopSSHAgentServer()
		}()
	}

	wg.Wait()
}

// TestConcurrentManagerGet verifies that Manager.Get is safe under
// concurrent access (run with -race to verify).
func TestConcurrentManagerGet(t *testing.T) {
	rt := &flexibleRuntime{done: make(chan struct{})}
	m := newEdgeCaseManager(t, rt)

	for i := 0; i < 5; i++ {
		r := &Run{
			ID:          fmt.Sprintf("run_%d", i),
			Name:        fmt.Sprintf("agent-%d", i),
			ContainerID: fmt.Sprintf("ctr-%d", i),
			State:       StateRunning,
			exitCh:      make(chan struct{}),
		}
		m.mu.Lock()
		m.runs[r.ID] = r
		m.mu.Unlock()
	}

	var wg sync.WaitGroup

	// Concurrent reads
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				id := fmt.Sprintf("run_%d", idx%5)
				r, err := m.Get(id)
				if err != nil {
					t.Errorf("Get(%q) failed: %v", id, err)
					return
				}
				if r.ID != id {
					t.Errorf("expected ID %q, got %q", id, r.ID)
				}
			}
		}(i)
	}

	wg.Wait()
}

// TestConcurrentManagerList verifies that Manager.List is safe under
// concurrent access with modifications (run with -race).
func TestConcurrentManagerList(t *testing.T) {
	rt := &flexibleRuntime{done: make(chan struct{})}
	m := newEdgeCaseManager(t, rt)

	for i := 0; i < 3; i++ {
		r := &Run{
			ID:          fmt.Sprintf("run_%d", i),
			Name:        fmt.Sprintf("agent-%d", i),
			ContainerID: fmt.Sprintf("ctr-%d", i),
			State:       StateRunning,
			exitCh:      make(chan struct{}),
		}
		m.mu.Lock()
		m.runs[r.ID] = r
		m.mu.Unlock()
	}

	var wg sync.WaitGroup

	// Concurrent List calls
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				runs := m.List()
				if len(runs) != 3 {
					t.Errorf("expected 3 runs, got %d", len(runs))
				}
			}
		}()
	}

	// Concurrent state modifications
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := fmt.Sprintf("run_%d", idx)
			r, _ := m.Get(id)
			if r != nil {
				for j := 0; j < 50; j++ {
					r.SetState(StateRunning)
				}
			}
		}(i)
	}

	wg.Wait()
}

// --- Privileged mode edge case tests ---

// TestDockerDependencyConfigDindPrivileged verifies that dind mode
// correctly sets the Privileged flag in container config.
func TestDockerDependencyConfigDindPrivileged(t *testing.T) {
	dindConfig := &DockerDependencyConfig{
		Mode:       deps.DockerModeDind,
		Privileged: true,
	}

	cfg := computeDockerModeConfig(dindConfig)
	if !cfg.Privileged {
		t.Error("dind mode should always produce Privileged=true")
	}
}

// TestDockerDependencyConfigHostNotPrivileged verifies that host mode
// never sets privileged.
func TestDockerDependencyConfigHostNotPrivileged(t *testing.T) {
	hostConfig := &DockerDependencyConfig{
		Mode:       deps.DockerModeHost,
		Privileged: false,
		GroupID:    "999",
		SocketMount: container.MountConfig{
			Source: "/var/run/docker.sock",
			Target: "/var/run/docker.sock",
		},
	}

	cfg := computeDockerModeConfig(hostConfig)
	if cfg.Privileged {
		t.Error("host mode should never produce Privileged=true")
	}
}

// TestDockerDependencyConfigNilSafe verifies that computeDockerModeConfig
// handles nil config without panicking.
func TestDockerDependencyConfigNilSafe(t *testing.T) {
	cfg := computeDockerModeConfig(nil)
	if cfg.Privileged {
		t.Error("nil config should produce Privileged=false")
	}
	if len(cfg.Mounts) != 0 {
		t.Error("nil config should produce empty mounts")
	}
}

// --- Log capture idempotency tests ---

// TestCaptureLogsIdempotent verifies that captureLogs can be called
// multiple times safely (only writes once).
func TestCaptureLogsIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := storage.NewRunStore(tmpDir, "run_idempotent")
	if err != nil {
		t.Fatal(err)
	}

	writeCount := 0
	rt := &flexibleRuntime{
		done: make(chan struct{}),
		containerLogsAll: func(context.Context, string) ([]byte, error) {
			writeCount++
			return []byte("test log line\n"), nil
		},
	}
	m := newEdgeCaseManager(t, rt)

	r := &Run{
		ID:          "run_idempotent",
		Name:        "idempotent",
		ContainerID: "ctr-idem",
		State:       StateStopped,
		Store:       store,
		exitCh:      make(chan struct{}),
	}
	close(r.exitCh)

	// Call captureLogs multiple times
	m.captureLogs(r)
	m.captureLogs(r)
	m.captureLogs(r)

	// Should only fetch logs once
	if writeCount != 1 {
		t.Errorf("expected ContainerLogsAll called once, got %d", writeCount)
	}

	// Verify logs file was created
	logsPath := filepath.Join(store.Dir(), "logs.jsonl")
	if _, err := os.Stat(logsPath); os.IsNotExist(err) {
		t.Error("logs.jsonl should exist after captureLogs")
	}
}

// TestCaptureLogsConcurrent verifies that multiple goroutines calling
// captureLogs simultaneously don't corrupt the log file.
func TestCaptureLogsConcurrent(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := storage.NewRunStore(tmpDir, "run_conc_logs")
	if err != nil {
		t.Fatal(err)
	}

	rt := &flexibleRuntime{
		done: make(chan struct{}),
		containerLogsAll: func(context.Context, string) ([]byte, error) {
			return []byte("concurrent log\n"), nil
		},
	}
	m := newEdgeCaseManager(t, rt)

	r := &Run{
		ID:          "run_conc_logs",
		Name:        "conc-logs",
		ContainerID: "ctr-conc",
		State:       StateStopped,
		Store:       store,
		exitCh:      make(chan struct{}),
	}
	close(r.exitCh)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.captureLogs(r)
		}()
	}
	wg.Wait()

	// Verify file exists and is not corrupted
	logsPath := filepath.Join(store.Dir(), "logs.jsonl")
	if _, err := os.Stat(logsPath); os.IsNotExist(err) {
		t.Error("logs.jsonl should exist")
	}
}

// TestCaptureLogsContainerLogsError verifies that captureLogs handles
// ContainerLogsAll errors gracefully (creates empty log file).
func TestCaptureLogsContainerLogsError(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := storage.NewRunStore(tmpDir, "run_log_err")
	if err != nil {
		t.Fatal(err)
	}

	rt := &flexibleRuntime{
		done: make(chan struct{}),
		containerLogsAll: func(context.Context, string) ([]byte, error) {
			return nil, errors.New("container not found")
		},
	}
	m := newEdgeCaseManager(t, rt)

	r := &Run{
		ID:          "run_log_err",
		Name:        "log-err",
		ContainerID: "ctr-missing",
		State:       StateStopped,
		Store:       store,
		exitCh:      make(chan struct{}),
	}
	close(r.exitCh)

	m.captureLogs(r)

	// Should still create empty logs.jsonl for audit completeness
	logsPath := filepath.Join(store.Dir(), "logs.jsonl")
	if _, err := os.Stat(logsPath); os.IsNotExist(err) {
		t.Error("logs.jsonl should be created even when container logs fail")
	}
}

// TestCaptureLogsSkipsInteractiveApple verifies that captureLogs is
// skipped for Apple containers in interactive mode (they use tee).
func TestCaptureLogsSkipsInteractiveApple(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := storage.NewRunStore(tmpDir, "run_apple_interactive")
	if err != nil {
		t.Fatal(err)
	}

	logsFetched := false
	rt := &flexibleRuntime{
		done:        make(chan struct{}),
		runtimeType: container.RuntimeApple,
		containerLogsAll: func(context.Context, string) ([]byte, error) {
			logsFetched = true
			return []byte("should not see this"), nil
		},
	}
	m := newEdgeCaseManager(t, rt)

	r := &Run{
		ID:          "run_apple_interactive",
		Name:        "apple-interactive",
		ContainerID: "ctr-apple",
		State:       StateStopped,
		Interactive: true,
		Store:       store,
		exitCh:      make(chan struct{}),
	}
	close(r.exitCh)

	m.captureLogs(r)

	if logsFetched {
		t.Error("captureLogs should skip ContainerLogsAll for Apple interactive runs")
	}
}

// --- monitorContainerExit tests ---

// TestMonitorContainerExitSetsStateFailed verifies that monitorContainerExit
// correctly sets the state to Failed when the container exits with non-zero code.
func TestMonitorContainerExitSetsStateFailed(t *testing.T) {
	// Use os.MkdirTemp to avoid cleanup race with background goroutine
	tmpDir, err := os.MkdirTemp("", "TestMonitorContainerExitFailed")
	if err != nil {
		t.Fatal(err)
	}

	store, err := storage.NewRunStore(tmpDir, "run_exit_fail")
	if err != nil {
		t.Fatal(err)
	}

	rt := &flexibleRuntime{
		done: make(chan struct{}),
		waitFn: func(_ context.Context, _ string) (int64, error) {
			return 1, nil // non-zero exit code
		},
	}
	m := newEdgeCaseManager(t, rt)

	r := &Run{
		ID:          "run_exit_fail",
		Name:        "exit-fail",
		ContainerID: "ctr-fail",
		State:       StateRunning,
		Store:       store,
		exitCh:      make(chan struct{}),
	}
	m.mu.Lock()
	m.runs[r.ID] = r
	m.mu.Unlock()

	// Run monitor in goroutine and wait for it to complete
	done := make(chan struct{})
	go func() {
		m.monitorContainerExit(context.Background(), r)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("monitorContainerExit did not complete in time")
	}

	if r.GetState() != StateFailed {
		t.Errorf("expected StateFailed, got %s", r.GetState())
	}

	// exitCh should be closed
	select {
	case <-r.exitCh:
		// good
	default:
		t.Error("exitCh should be closed after container exits")
	}
}

// TestMonitorContainerExitSetsStateStopped verifies that monitorContainerExit
// correctly sets the state to Stopped when the container exits with code 0.
func TestMonitorContainerExitSetsStateStopped(t *testing.T) {
	// Use os.MkdirTemp to avoid cleanup race with background goroutine
	tmpDir, err := os.MkdirTemp("", "TestMonitorContainerExitStopped")
	if err != nil {
		t.Fatal(err)
	}

	store, err := storage.NewRunStore(tmpDir, "run_exit_ok")
	if err != nil {
		t.Fatal(err)
	}

	rt := &flexibleRuntime{
		done: make(chan struct{}),
		waitFn: func(_ context.Context, _ string) (int64, error) {
			return 0, nil // clean exit
		},
	}
	m := newEdgeCaseManager(t, rt)

	r := &Run{
		ID:          "run_exit_ok",
		Name:        "exit-ok",
		ContainerID: "ctr-ok",
		State:       StateRunning,
		Store:       store,
		exitCh:      make(chan struct{}),
	}
	m.mu.Lock()
	m.runs[r.ID] = r
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.monitorContainerExit(context.Background(), r)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("monitorContainerExit did not complete in time")
	}

	if r.GetState() != StateStopped {
		t.Errorf("expected StateStopped, got %s", r.GetState())
	}
}

// --- lastNLines edge case tests ---

func TestLastNLinesEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
		n     int
		want  string
	}{
		{"empty string", "", 5, ""},
		{"zero lines", "hello\nworld\n", 0, ""},
		{"negative lines", "hello\nworld\n", -1, ""},
		{"single line no newline", "hello", 1, "hello"},
		{"single line with newline", "hello\n", 1, "hello\n"},
		{"more lines requested than exist", "a\nb\nc\n", 10, "a\nb\nc\n"},
		{"exact lines", "a\nb\nc\n", 3, "a\nb\nc\n"},
		{"last 1 of 3", "a\nb\nc\n", 1, "c\n"},
		{"last 2 of 3", "a\nb\nc\n", 2, "b\nc\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lastNLines(tt.input, tt.n)
			if got != tt.want {
				t.Errorf("lastNLines(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
			}
		})
	}
}

// TestMonitorContainerExitWaitError verifies that monitorContainerExit handles
// WaitContainer errors (e.g., context canceled or runtime error) correctly.
func TestMonitorContainerExitWaitError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "TestMonitorContainerExitWaitError")
	if err != nil {
		t.Fatal(err)
	}

	store, err := storage.NewRunStore(tmpDir, "run_wait_err")
	if err != nil {
		t.Fatal(err)
	}

	rt := &flexibleRuntime{
		done: make(chan struct{}),
		waitFn: func(_ context.Context, _ string) (int64, error) {
			return 0, errors.New("connection lost to container runtime")
		},
	}
	m := newEdgeCaseManager(t, rt)

	r := &Run{
		ID:          "run_wait_err",
		Name:        "wait-err",
		ContainerID: "ctr-wait",
		State:       StateRunning,
		Store:       store,
		exitCh:      make(chan struct{}),
	}
	m.mu.Lock()
	m.runs[r.ID] = r
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.monitorContainerExit(context.Background(), r)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("monitorContainerExit did not complete in time")
	}

	// WaitContainer error + exit code 0 should still mark as failed
	if r.GetState() != StateFailed {
		t.Errorf("expected StateFailed when WaitContainer errors, got %s", r.GetState())
	}

	// exitCh should be closed
	select {
	case <-r.exitCh:
		// good
	default:
		t.Error("exitCh should be closed after WaitContainer error")
	}
}

// TestConcurrentStopAndMonitorExit_CaptureLogsIdempotent verifies that
// captureLogs and stopProxyServer/stopSSHAgent are safe when called
// concurrently from both Stop() and monitorContainerExit().
//
// The state mutation race in Stop() (where r.State and r.StoppedAt were written
// under m.mu instead of r.stateMu) has been fixed: Stop() now uses
// r.SetStateWithTime() which correctly acquires r.stateMu. Start() and
// StartAttached() also use the proper setters (SetState, SetStateWithError,
// SetStateWithTime) for all state mutations.
func TestConcurrentStopAndMonitorExit_CaptureLogsIdempotent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "TestConcurrentStopAndMonitor")
	if err != nil {
		t.Fatal(err)
	}

	store, err := storage.NewRunStore(tmpDir, "run_race_test")
	if err != nil {
		t.Fatal(err)
	}

	rt := &flexibleRuntime{
		done: make(chan struct{}),
		containerLogsAll: func(context.Context, string) ([]byte, error) {
			return []byte("test log\n"), nil
		},
	}
	m := newEdgeCaseManager(t, rt)

	r := &Run{
		ID:          "run_race_test",
		Name:        "race-test",
		ContainerID: "ctr-race",
		State:       StateStopped,
		Store:       store,
		exitCh:      make(chan struct{}),
	}
	close(r.exitCh)

	// Call captureLogs and stopProxyServer concurrently from multiple goroutines
	// simulating the overlap between Stop() and monitorContainerExit()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.captureLogs(r)
			_ = r.stopProxyServer(context.Background())
			_ = r.stopSSHAgentServer()
		}()
	}
	wg.Wait()

	// Verify logs file was created exactly once
	logsPath := filepath.Join(store.Dir(), "logs.jsonl")
	if _, statErr := os.Stat(logsPath); os.IsNotExist(statErr) {
		t.Error("logs.jsonl should exist after concurrent captureLogs")
	}
}

// TestCaptureLogsSkipsWhenAlreadyCaptured verifies that captureLogs is a
// no-op when logsCaptured is already set and the log file exists (e.g.,
// because interactive mode tee'd output to the log file during the run).
func TestCaptureLogsSkipsWhenAlreadyCaptured(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := storage.NewRunStore(tmpDir, "run_already_captured")
	if err != nil {
		t.Fatal(err)
	}

	fetchCount := 0
	rt := &flexibleRuntime{
		done: make(chan struct{}),
		containerLogsAll: func(context.Context, string) ([]byte, error) {
			fetchCount++
			return []byte("fetched log\n"), nil
		},
	}
	m := newEdgeCaseManager(t, rt)

	r := &Run{
		ID:          "run_already_captured",
		Name:        "already-captured",
		ContainerID: "ctr-ac",
		State:       StateStopped,
		Store:       store,
		exitCh:      make(chan struct{}),
	}
	close(r.exitCh)

	// Simulate what interactive mode does: set logsCaptured and create the file
	r.logsCaptured.Store(true)
	lw, lwErr := store.LogWriter()
	if lwErr != nil {
		t.Fatal(lwErr)
	}
	lw.Write([]byte("pre-existing log line\n"))
	lw.Close()

	// captureLogs should see logsCaptured=true + existing file and skip
	m.captureLogs(r)

	if fetchCount > 0 {
		t.Errorf("ContainerLogsAll should not have been called when logsCaptured is already set, but was called %d times", fetchCount)
	}
}

// TestMonitorContainerExitAlreadyStopped verifies that monitorContainerExit
// does not change state if the run was already stopped (e.g., by Stop()).
func TestMonitorContainerExitAlreadyStopped(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "TestMonitorExitAlreadyStopped")
	if err != nil {
		t.Fatal(err)
	}

	store, err := storage.NewRunStore(tmpDir, "run_already_stopped")
	if err != nil {
		t.Fatal(err)
	}

	rt := &flexibleRuntime{
		done: make(chan struct{}),
		waitFn: func(_ context.Context, _ string) (int64, error) {
			return 0, nil
		},
	}
	m := newEdgeCaseManager(t, rt)

	stoppedAt := time.Now().Add(-1 * time.Minute)
	r := &Run{
		ID:          "run_already_stopped",
		Name:        "already-stopped",
		ContainerID: "ctr-already",
		State:       StateStopped,
		StoppedAt:   stoppedAt,
		Store:       store,
		exitCh:      make(chan struct{}),
	}
	m.mu.Lock()
	m.runs[r.ID] = r
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.monitorContainerExit(context.Background(), r)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("monitorContainerExit did not complete in time")
	}

	// State should remain Stopped (not changed to Failed or re-Stopped)
	if r.GetState() != StateStopped {
		t.Errorf("expected state to remain StateStopped, got %s", r.GetState())
	}
}
