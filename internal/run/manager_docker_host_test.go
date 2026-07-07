package run

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
