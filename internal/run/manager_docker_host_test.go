package run

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/majorcontext/moat/internal/container"
)

// newFakeDockerAPIServer starts an httptest server serving just enough of the
// Docker Engine API for client.Client's version negotiation, Ping, and
// ServerVersion calls, so container.RuntimePool.GetDockerAt can construct and
// ping a real docker client against it without a live daemon. Mirrors the
// helper of the same name in internal/container/detect_test.go.
func newFakeDockerAPIServer(t *testing.T) *httptest.Server {
	t.Helper()

	version := types.Version{APIVersion: "1.44", Version: "24.0.0"}
	body, err := json.Marshal(version)
	if err != nil {
		t.Fatalf("marshal version: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/version") {
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/_ping") {
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestRuntimeForRunDockerWithoutDockerHost verifies the unchanged path:
// a docker run with no recorded DockerHost (the default-socket case,
// including all runs persisted before DockerHost existed) resolves through
// the pool's ordinary Get(), returning the default runtime.
func TestRuntimeForRunDockerWithoutDockerHost(t *testing.T) {
	stub := &stubRuntime{}
	m := mgrWithRuntime(stub)

	r := &Run{Runtime: string(container.RuntimeDocker)}
	rt, err := m.runtimeForRun(r)
	if err != nil {
		t.Fatalf("runtimeForRun: %v", err)
	}
	if rt != container.Runtime(stub) {
		t.Fatal("expected runtimeForRun to return the pool's default runtime when DockerHost is empty")
	}
}

// TestRuntimeForRunDockerWithDockerHost is the companion case: a docker run
// recorded against a non-default DOCKER_HOST (podman or Rancher Desktop)
// must resolve to a runtime pinned to that host via GetDockerAt, not the
// pool's default runtime.
func TestRuntimeForRunDockerWithDockerHost(t *testing.T) {
	srv := newFakeDockerAPIServer(t)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing server URL: %v", err)
	}
	host := "tcp://" + u.Host

	stub := &stubRuntime{}
	m := mgrWithRuntime(stub)

	r := &Run{Runtime: string(container.RuntimeDocker), DockerHost: host}
	rt, err := m.runtimeForRun(r)
	if err != nil {
		t.Fatalf("runtimeForRun: %v", err)
	}
	if rt == container.Runtime(stub) {
		t.Fatal("expected runtimeForRun to route to a host-pinned runtime, not the pool default")
	}
	if rt.Type() != container.RuntimeDocker {
		t.Fatalf("Type() = %v, want %v", rt.Type(), container.RuntimeDocker)
	}
}

// TestRecordedDockerHost_DockerRuntime pins the creation-side decision: a
// DockerRuntime records its resolved DaemonHost, non-empty even with
// DOCKER_HOST unset, guarding against regressing to the raw env var.
func TestRecordedDockerHost_DockerRuntime(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://this-is-not-the-runtimes-endpoint:9999")

	srv := newFakeDockerAPIServer(t)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing server URL: %v", err)
	}
	host := "tcp://" + u.Host

	rt, err := container.NewDockerRuntimeWithHost(host, false)
	if err != nil {
		t.Fatalf("NewDockerRuntimeWithHost: %v", err)
	}
	defer rt.Close()

	got := recordedDockerHost(rt)
	if got == "" {
		t.Fatal("recordedDockerHost returned empty for a docker runtime; want the runtime's resolved endpoint")
	}
	if got != rt.DaemonHost() {
		t.Fatalf("recordedDockerHost = %q, want rt.DaemonHost() = %q", got, rt.DaemonHost())
	}
	if got == os.Getenv("DOCKER_HOST") {
		t.Fatalf("recordedDockerHost returned the raw DOCKER_HOST env var (%q) instead of the runtime's actual endpoint", got)
	}
}

// TestRecordedDockerHost_NonDockerRuntime is the companion case: a
// non-docker runtime (Apple containers, or any Runtime that isn't a
// *container.DockerRuntime) has no Docker-API endpoint to record.
func TestRecordedDockerHost_NonDockerRuntime(t *testing.T) {
	stub := &stubRuntime{}
	if got := recordedDockerHost(stub); got != "" {
		t.Fatalf("recordedDockerHost(non-docker runtime) = %q, want empty", got)
	}
}

// newFakePodmanAPIServer is like newFakeDockerAPIServer, but its /version
// response carries the "Podman Engine" component podman's real compat API
// reports — the marker DockerRuntime.EngineName uses to identify podman.
func newFakePodmanAPIServer(t *testing.T) *httptest.Server {
	t.Helper()

	version := types.Version{
		APIVersion: "1.44",
		Version:    "4.9.0",
		Components: []types.ComponentVersion{{Name: "Podman Engine", Version: "4.9.0"}},
	}
	body, err := json.Marshal(version)
	if err != nil {
		t.Fatalf("marshal version: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/version") {
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/_ping") {
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestRecordedEngine_Docker pins the creation-side identity call: a
// DockerRuntime backed by a real-Docker-shaped compat API asks the engine via
// EngineName and records "docker" — not a guess derived from the endpoint.
func TestRecordedEngine_Docker(t *testing.T) {
	srv := newFakeDockerAPIServer(t)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing server URL: %v", err)
	}
	host := "tcp://" + u.Host

	rt, err := container.NewDockerRuntimeWithHost(host, false)
	if err != nil {
		t.Fatalf("NewDockerRuntimeWithHost: %v", err)
	}
	defer rt.Close()

	if got := recordedEngine(context.Background(), rt); got != "docker" {
		t.Fatalf("recordedEngine = %q, want %q", got, "docker")
	}
}

// TestRecordedEngine_Podman is the companion case: a DockerRuntime backed by
// a podman-shaped compat API (the "Podman Engine" component marker) records
// "podman" — the exact scenario the path-sniffing heuristic could miss (a
// podman machine whose name lacks "podman", or podman over tcp://).
func TestRecordedEngine_Podman(t *testing.T) {
	srv := newFakePodmanAPIServer(t)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing server URL: %v", err)
	}
	// Deliberately a tcp:// endpoint with no "podman" in the path at all, so a
	// path-sniffing heuristic would have mislabeled this "docker".
	host := "tcp://" + u.Host

	rt, err := container.NewDockerRuntimeWithHost(host, false)
	if err != nil {
		t.Fatalf("NewDockerRuntimeWithHost: %v", err)
	}
	defer rt.Close()

	if got := recordedEngine(context.Background(), rt); got != "podman" {
		t.Fatalf("recordedEngine = %q, want %q", got, "podman")
	}
}

// TestRecordedEngine_NonDockerRuntime is the companion case: a non-docker
// runtime has no engine identity to probe.
func TestRecordedEngine_NonDockerRuntime(t *testing.T) {
	stub := &stubRuntime{}
	if got := recordedEngine(context.Background(), stub); got != "" {
		t.Fatalf("recordedEngine(non-docker runtime) = %q, want empty", got)
	}
}

// TestRecordedEngine_ProbeFailureNeverPanics guards the "must never fail
// creation" contract at the recordedEngine call site: a runtime whose
// EngineName call errors (server closed) must yield "" rather than a panic
// or propagated error.
func TestRecordedEngine_ProbeFailureNeverPanics(t *testing.T) {
	srv := newFakeDockerAPIServer(t)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing server URL: %v", err)
	}
	host := "tcp://" + u.Host

	rt, err := container.NewDockerRuntimeWithHost(host, false)
	if err != nil {
		t.Fatalf("NewDockerRuntimeWithHost: %v", err)
	}
	defer rt.Close()
	srv.Close() // Engine probe will now fail to connect.

	if got := recordedEngine(context.Background(), rt); got != "" {
		t.Fatalf("recordedEngine after server close = %q, want empty", got)
	}
}

// TestRuntimeForEndpoint_RoutingDrift is the drift-guard for the routing
// helper shared by runtimeForRun and loadPersistedRuns: a recorded endpoint
// pins to it, an empty one falls back to the pool default.
func TestRuntimeForEndpoint_RoutingDrift(t *testing.T) {
	srv := newFakeDockerAPIServer(t)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing server URL: %v", err)
	}
	host := "tcp://" + u.Host

	stub := &stubRuntime{}
	m := mgrWithRuntime(stub)

	t.Run("non-empty dockerHost routes to the pinned endpoint", func(t *testing.T) {
		rt, err := m.runtimeForEndpoint(context.Background(), string(container.RuntimeDocker), host)
		if err != nil {
			t.Fatalf("runtimeForEndpoint: %v", err)
		}
		if rt == container.Runtime(stub) {
			t.Fatal("expected routing to the host-pinned runtime, not the pool default")
		}
		dr, ok := rt.(*container.DockerRuntime)
		if !ok {
			t.Fatalf("expected a *container.DockerRuntime, got %T", rt)
		}
		if dr.DaemonHost() != host {
			t.Fatalf("DaemonHost() = %q, want %q", dr.DaemonHost(), host)
		}
	})

	t.Run("empty dockerHost routes to the pool default", func(t *testing.T) {
		rt, err := m.runtimeForEndpoint(context.Background(), string(container.RuntimeDocker), "")
		if err != nil {
			t.Fatalf("runtimeForEndpoint: %v", err)
		}
		if rt != container.Runtime(stub) {
			t.Fatal("expected routing to the pool default when dockerHost is empty")
		}
	})
}

// TestRuntimeForRun_ReproducedScenario recreates the reported bug: a run
// created on one docker-type engine, reconnected in a process whose pool
// default is a different one (`MOAT_RUNTIME=podman moat stop <run>`).
// runtimeForRun must resolve by recorded endpoint, not the pool default.
func TestRuntimeForRun_ReproducedScenario(t *testing.T) {
	// "recorded" simulates the real Docker daemon the run was created against.
	recordedSrv := newFakeDockerAPIServer(t)
	recordedURL, err := url.Parse(recordedSrv.URL)
	if err != nil {
		t.Fatalf("parsing recorded server URL: %v", err)
	}
	recordedHost := "tcp://" + recordedURL.Host

	// "podmanDefault" simulates a podman-shaped engine that a later process
	// (MOAT_RUNTIME=podman) resolves as its pool default. It is a genuine
	// *container.DockerRuntime (Type() == "docker"), just pointed at a
	// different endpoint than the run was actually created on.
	podmanSrv := newFakeDockerAPIServer(t)
	podmanURL, err := url.Parse(podmanSrv.URL)
	if err != nil {
		t.Fatalf("parsing podman server URL: %v", err)
	}
	podmanHost := "tcp://" + podmanURL.Host

	podmanDefault, err := container.NewDockerRuntimeWithHost(podmanHost, false)
	if err != nil {
		t.Fatalf("NewDockerRuntimeWithHost(podman default): %v", err)
	}
	defer podmanDefault.Close()

	m := mgrWithRuntime(podmanDefault)

	r := &Run{Runtime: string(container.RuntimeDocker), DockerHost: recordedHost}
	rt, err := m.runtimeForRun(r)
	if err != nil {
		t.Fatalf("runtimeForRun: %v", err)
	}

	dr, ok := rt.(*container.DockerRuntime)
	if !ok {
		t.Fatalf("expected a *container.DockerRuntime, got %T", rt)
	}
	if dr.DaemonHost() == podmanHost {
		t.Fatal("runtimeForRun returned the mismatched pool default (podman-shaped engine) instead of the recorded endpoint — this is the reproduced bug")
	}
	if dr.DaemonHost() != recordedHost {
		t.Fatalf("DaemonHost() = %q, want the recorded endpoint %q", dr.DaemonHost(), recordedHost)
	}
}
