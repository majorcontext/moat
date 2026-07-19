package container

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/majorcontext/moat/internal/ui"
)

func TestGVisorAvailable(t *testing.T) {
	// Actual gVisor detection depends on Docker daemon configuration.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Should not panic, should return bool
	_ = GVisorAvailable(ctx)
}

func TestDefaultRuntimeOptions(t *testing.T) {
	opts := DefaultRuntimeOptions()

	if runtime.GOOS == "linux" {
		if !opts.Sandbox {
			t.Error("expected sandbox=true on Linux")
		}
	} else {
		if opts.Sandbox {
			t.Errorf("expected sandbox=false on %s", runtime.GOOS)
		}
	}
}

func TestDefaultRuntimeOptionsNoSandboxEnv(t *testing.T) {
	t.Setenv("MOAT_NO_SANDBOX", "1")
	opts := DefaultRuntimeOptions()
	if opts.Sandbox {
		t.Error("expected sandbox=false when MOAT_NO_SANDBOX=1")
	}
}

func TestIsAppleSilicon(t *testing.T) {
	got := IsAppleSilicon()
	want := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
	if got != want {
		t.Errorf("IsAppleSilicon() = %v, want %v", got, want)
	}
}

func TestNewRuntimeWithOptionsUnknownOverride(t *testing.T) {
	t.Setenv("MOAT_RUNTIME", "unknown")
	_, err := NewRuntimeWithOptions(RuntimeOptions{})
	if err == nil {
		t.Fatal("expected error for unknown MOAT_RUNTIME value")
	}
	if want := `unknown MOAT_RUNTIME value "unknown"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q should contain %q", err.Error(), want)
	}
}

func TestNewRuntimeWithOptionsDockerOverrideNoDocker(t *testing.T) {
	t.Setenv("MOAT_RUNTIME", "docker")
	_, err := NewRuntimeWithOptions(RuntimeOptions{Sandbox: false})
	// On systems without Docker, this should fail with a helpful message
	if err != nil {
		msg := err.Error()
		if !strings.Contains(msg, "Docker runtime requested") {
			t.Errorf("error should mention Docker was requested, got: %s", msg)
		}
		if !strings.Contains(msg, "MOAT_RUNTIME=apple") {
			t.Errorf("error should suggest MOAT_RUNTIME=apple, got: %s", msg)
		}
	}
	// On systems with Docker, this will succeed — that's fine too
}

func TestAppleContainerAvailable(t *testing.T) {
	// This test verifies the function doesn't panic.
	// Result depends on whether the 'container' CLI is installed.
	_ = appleContainerAvailable()
}

func TestTryAppleRuntimeNoContainerCLI(t *testing.T) {
	// Clear PATH to simulate missing container CLI
	t.Setenv("PATH", "/nonexistent")

	rt, reason := tryAppleRuntime()
	if rt != nil {
		t.Error("expected nil runtime when container CLI is not in PATH")
	}
	if reason == "" {
		t.Error("expected non-empty reason when container CLI is missing")
	}
	if !strings.Contains(reason, "not found in PATH") {
		t.Errorf("reason should mention PATH, got: %s", reason)
	}
}

func TestAlternativeDockerSocketPaths(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("alternative socket paths are macOS-only")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	candidates := alternativeDockerSockets()
	wantPath := filepath.Join(home, ".rd", "docker.sock")
	for _, c := range candidates {
		if c.name == "Rancher Desktop" && c.path != wantPath {
			t.Errorf("Rancher Desktop: path = %q, want %q", c.path, wantPath)
		}
	}
}

func TestPodmanSocketCandidatesDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only podman socket layout")
	}

	// Point TMPDIR at a scratch dir containing a fake podman machine socket,
	// so the glob in podmanSocketCandidates has something deterministic to find.
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir+"/")

	podmanDir := filepath.Join(dir, "podman")
	if err := os.MkdirAll(podmanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sockPath := filepath.Join(podmanDir, "podman-machine-default-api.sock")
	if err := os.WriteFile(sockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	candidates := podmanSocketCandidates()
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d: %+v", len(candidates), candidates)
	}
	if candidates[0].path != sockPath {
		t.Errorf("path = %q, want %q", candidates[0].path, sockPath)
	}
	if candidates[0].name != "Podman machine" {
		t.Errorf("name = %q, want %q", candidates[0].name, "Podman machine")
	}
}

func TestPodmanSocketCandidatesDarwinNoMachine(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only podman socket layout")
	}

	t.Setenv("TMPDIR", t.TempDir()+"/")

	if candidates := podmanSocketCandidates(); len(candidates) != 0 {
		t.Errorf("expected no candidates when no podman machine socket exists, got %+v", candidates)
	}
}

func TestPodmanSocketCandidatesLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only podman socket layout")
	}

	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	candidates := podmanSocketCandidates()

	wantRootless := "/run/user/1000/podman/podman.sock"
	wantRootful := "/run/podman/podman.sock"
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d: %+v", len(candidates), candidates)
	}
	if candidates[0].path != wantRootless {
		t.Errorf("rootless path = %q, want %q", candidates[0].path, wantRootless)
	}
	if candidates[1].path != wantRootful {
		t.Errorf("rootful path = %q, want %q", candidates[1].path, wantRootful)
	}
}

func TestPodmanSocketCandidatesLinuxNoXDGRuntimeDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only podman socket layout")
	}

	// Redirect the uid-fallback seam so this test doesn't depend on the
	// actual invoking uid (sudo/cron/CI contexts lack XDG_RUNTIME_DIR but
	// still have podman's socket under /run/user/<uid>).
	origFallback := xdgRuntimeDirFallback
	xdgRuntimeDirFallback = func() string { return "/run/user/9999" }
	t.Cleanup(func() { xdgRuntimeDirFallback = origFallback })

	t.Setenv("XDG_RUNTIME_DIR", "")
	candidates := podmanSocketCandidates()

	wantRootless := "/run/user/9999/podman/podman.sock"
	wantRootful := "/run/podman/podman.sock"
	if len(candidates) != 2 {
		t.Fatalf("expected the uid-fallback rootless candidate plus rootful when XDG_RUNTIME_DIR is unset, got %+v", candidates)
	}
	if candidates[0].path != wantRootless {
		t.Errorf("rootless path = %q, want %q", candidates[0].path, wantRootless)
	}
	if candidates[1].path != wantRootful {
		t.Errorf("rootful path = %q, want %q", candidates[1].path, wantRootful)
	}
}

func TestXDGRuntimeDirFallbackUsesUID(t *testing.T) {
	// The real (non-overridden) fallback must derive the path from the
	// current process's uid — the systemd/podman convention — not a fixed
	// or empty value.
	want := fmt.Sprintf("/run/user/%d", os.Getuid())
	if got := xdgRuntimeDirFallback(); got != want {
		t.Errorf("xdgRuntimeDirFallback() = %q, want %q", got, want)
	}
}

func TestAlternativeDockerSocketsIncludesPodman(t *testing.T) {
	// alternativeDockerSockets should append podman candidates after any
	// platform-specific third-party sockets (Rancher Desktop on macOS keeps
	// precedence). This just checks podman candidates aren't dropped.
	got := alternativeDockerSockets()
	want := podmanSocketCandidates()
	if len(got) < len(want) {
		t.Fatalf("alternativeDockerSockets() returned fewer entries (%d) than podmanSocketCandidates() (%d)", len(got), len(want))
	}
	gotTail := got[len(got)-len(want):]
	for i := range want {
		if gotTail[i] != want[i] {
			t.Errorf("alternativeDockerSockets() tail[%d] = %+v, want %+v", i, gotTail[i], want[i])
		}
	}
}

func TestVersionIsPodman(t *testing.T) {
	tests := []struct {
		name       string
		components []string
		want       bool
	}{
		{"podman engine", []string{"Podman Engine"}, true},
		{"docker engine", []string{"Engine"}, false},
		{"docker desktop style", []string{"Engine", "containerd", "runc", "docker-init"}, false},
		{"empty", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := types.Version{}
			for _, name := range tt.components {
				v.Components = append(v.Components, types.ComponentVersion{Name: name})
			}
			if got := versionIsPodman(v); got != tt.want {
				t.Errorf("versionIsPodman(%v) = %v, want %v", tt.components, got, tt.want)
			}
		})
	}
}

func TestNewRuntimeWithOptionsPodmanOverrideNoPodman(t *testing.T) {
	// Without a live podman socket (and no DOCKER_HOST), MOAT_RUNTIME=podman
	// should fail with an actionable hint rather than silently falling back
	// to another runtime.
	t.Setenv("MOAT_RUNTIME", "podman")
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("HOME", t.TempDir())
	if runtime.GOOS == "darwin" {
		t.Setenv("TMPDIR", t.TempDir()+"/")
	}
	if runtime.GOOS == "linux" {
		t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	}

	_, err := NewRuntimeWithOptions(RuntimeOptions{})
	if err == nil {
		t.Skip("a podman socket appears to be reachable on this machine; skipping negative-path assertion")
	}
	if !strings.Contains(err.Error(), "podman") {
		t.Errorf("error should mention podman, got: %v", err)
	}
	if !strings.Contains(err.Error(), "podman machine start") && !strings.Contains(err.Error(), "podman.socket") {
		t.Errorf("error should include a start hint, got: %v", err)
	}
}

func TestTryAlternativeDockerSocketsNoSockets(t *testing.T) {
	// Point HOME (Rancher Desktop), TMPDIR (podman machine on macOS), and
	// XDG_RUNTIME_DIR (podman rootless on Linux) at empty scratch dirs so no
	// candidate paths exist, isolating this from any real Docker/podman
	// tooling running on the test host.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir()+"/")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	// The Linux rootful podman candidate (/run/podman/podman.sock) is a fixed
	// path that can't be neutralized via env vars — redirect it to a scratch
	// path so this test stays hermetic even on a Linux host with rootful
	// podman running.
	origRootful := podmanRootfulSocket
	podmanRootfulSocket = filepath.Join(t.TempDir(), "podman.sock")
	t.Cleanup(func() { podmanRootfulSocket = origRootful })

	rt := tryAlternativeDockerSockets(false)
	if rt != nil {
		t.Error("expected nil when no alternative sockets exist")
	}
}

// newFakeDockerAPIServer starts an httptest server that serves just enough of
// the Docker Engine API for client.Client's version negotiation, Ping, and
// ServerVersion calls: HEAD/GET /_ping and GET .../version. If podman is
// true, the version response includes podman's compat-API marker component
// ("Podman Engine"), as podman's real compat API does.
func newFakeDockerAPIServer(t *testing.T, podman bool) *httptest.Server {
	t.Helper()

	version := types.Version{APIVersion: "1.44", Version: "24.0.0"}
	if podman {
		version.Components = []types.ComponentVersion{{Name: "Podman Engine", Version: "4.9.0"}}
	}
	body, err := json.Marshal(version)
	if err != nil {
		t.Fatalf("marshal version: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/_ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("API-Version", "1.44")
		w.WriteHeader(http.StatusOK)
	})
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

func TestNewRuntimeWithOptionsPodmanOverrideDockerHostNonPodman(t *testing.T) {
	srv := newFakeDockerAPIServer(t, false)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing server URL: %v", err)
	}

	t.Setenv("MOAT_RUNTIME", "podman")
	t.Setenv("DOCKER_HOST", "tcp://"+u.Host)

	_, err = NewRuntimeWithOptions(RuntimeOptions{Sandbox: false})
	if err == nil {
		t.Fatal("expected error when DOCKER_HOST points at a non-podman engine")
	}
	if !strings.Contains(err.Error(), "non-podman engine") {
		t.Errorf("error should identify a non-podman engine, got: %v", err)
	}
}

func TestNewRuntimeWithOptionsPodmanOverrideDockerHostPodman(t *testing.T) {
	srv := newFakeDockerAPIServer(t, true)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing server URL: %v", err)
	}

	t.Setenv("MOAT_RUNTIME", "podman")
	t.Setenv("DOCKER_HOST", "tcp://"+u.Host)

	rt, err := NewRuntimeWithOptions(RuntimeOptions{Sandbox: false})
	if err != nil {
		t.Fatalf("expected success when DOCKER_HOST points at podman's compat API, got: %v", err)
	}
	if rt.Type() != RuntimeDocker {
		t.Errorf("Type() = %v, want %v (podman is served via the Docker runtime)", rt.Type(), RuntimeDocker)
	}
}

// TestNewRuntimeWithOptionsPodmanOverrideCandidateRejectsNonPodman is the
// auto-probe companion to TestNewRuntimeWithOptionsPodmanOverrideDockerHostNonPodman:
// with DOCKER_HOST unset, newPodmanRuntimeWithPing's candidate probe
// (tryDockerSocketCandidatesVerified over podmanSocketCandidates, see
// detect.go's newPodmanRuntimeWithPing) must reject a Docker-flavored engine
// sitting on a podman candidate socket rather than trusting the path alone —
// candidate-list membership is not proof of engine identity.
func TestNewRuntimeWithOptionsPodmanOverrideCandidateRejectsNonPodman(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("unix-socket-based candidate probing is unix/darwin-only")
	}

	t.Setenv("MOAT_RUNTIME", "podman")
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("HOME", t.TempDir())

	var podmanSockPath string
	switch runtime.GOOS {
	case "darwin":
		dir := shortTempDir(t)
		t.Setenv("TMPDIR", dir+"/")
		podmanDir := filepath.Join(dir, "podman")
		if err := os.MkdirAll(podmanDir, 0o755); err != nil {
			t.Fatal(err)
		}
		podmanSockPath = filepath.Join(podmanDir, "podman-machine-default-api.sock")
	case "linux":
		t.Setenv("XDG_RUNTIME_DIR", "")
		dir := shortTempDir(t)
		origRootful := podmanRootfulSocket
		podmanRootfulSocket = filepath.Join(dir, "podman.sock")
		t.Cleanup(func() { podmanRootfulSocket = origRootful })
		podmanSockPath = podmanRootfulSocket
	}

	// podman=false: the fake engine answers /version without podman's
	// "Podman Engine" component marker, as a plain Docker-compatible engine
	// (that happened to be listening on a podman-shaped path) would.
	serveFakeDockerAPIUnixSocket(t, podmanSockPath, false)

	_, err := NewRuntimeWithOptions(RuntimeOptions{Sandbox: false})
	if err == nil {
		t.Fatal("MOAT_RUNTIME=podman should not accept a non-podman engine found via candidate probing")
	}
	if !strings.Contains(err.Error(), "podman") {
		t.Errorf("error should mention podman, got: %v", err)
	}
}

func TestIsPodmanEngineDoesNotCacheError(t *testing.T) {
	// Server that fails ServerVersion (/version) until toldRecovered flips,
	// but always answers /_ping so NewDockerRuntime/Ping succeed regardless.
	// This lets us call IsPodmanEngine twice against the *same* runtime: once
	// while the version endpoint is broken (must return an error and must not
	// cache "false"), and once after it recovers (must then report true).
	var recovered bool
	version := types.Version{
		APIVersion: "1.44",
		Version:    "24.0.0",
		Components: []types.ComponentVersion{{Name: "Podman Engine", Version: "4.9.0"}},
	}
	body, err := json.Marshal(version)
	if err != nil {
		t.Fatalf("marshal version: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/_ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("API-Version", "1.44")
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_ping") {
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/version") {
			if !recovered {
				http.Error(w, "temporarily unavailable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
			return
		}
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing server URL: %v", err)
	}
	t.Setenv("DOCKER_HOST", "tcp://"+u.Host)

	rt, err := NewDockerRuntime(false)
	if err != nil {
		t.Fatalf("NewDockerRuntime: %v", err)
	}
	defer rt.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := rt.IsPodmanEngine(ctx); err == nil {
		t.Fatal("expected an error while the version endpoint is broken")
	}

	recovered = true
	isPodman, err := rt.IsPodmanEngine(ctx)
	if err != nil {
		t.Fatalf("expected the earlier error not to be cached, got: %v", err)
	}
	if !isPodman {
		t.Error("expected IsPodmanEngine to report true once the version endpoint recovers")
	}
}

// TestSocketStatFollowsSymlink verifies that os.Stat (not Lstat) is the correct
// call for socket detection. On macOS, ~/.rd/docker.sock is a symlink to the
// actual socket. os.Lstat returns ModeSymlink (not ModeSocket), causing
// detection to silently skip the candidate. os.Stat follows the symlink.
// This test has no network dependency and no timeout.
func TestSocketStatFollowsSymlink(t *testing.T) {
	dir, err := os.MkdirTemp("", "m")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	sockPath := filepath.Join(dir, "real.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("creating socket: %v", err)
	}
	defer ln.Close()

	linkPath := filepath.Join(dir, "link.sock")
	if err := os.Symlink(sockPath, linkPath); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	// os.Lstat on a symlink-to-socket returns ModeSymlink, not ModeSocket.
	linfo, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	if linfo.Mode()&os.ModeSocket != 0 {
		t.Error("os.Lstat should NOT report ModeSocket for a symlink to a socket")
	}

	// os.Stat follows the symlink and correctly reports ModeSocket.
	info, err := os.Stat(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Error("os.Stat should report ModeSocket when following a symlink to a socket")
	}
}

// serveFakeDockerAPIUnixSocket starts an HTTP server on a unix socket at
// path, serving the same minimal Docker Engine API surface as
// newFakeDockerAPIServer (HEAD/GET /_ping, GET .../version), so it can stand
// in for a live podman/docker socket at a specific filesystem path (rather
// than httptest's TCP listener). path's parent directory must already
// exist. The server is stopped via t.Cleanup.
func serveFakeDockerAPIUnixSocket(t *testing.T, path string, podman bool) {
	t.Helper()

	version := types.Version{APIVersion: "1.44", Version: "24.0.0"}
	if podman {
		version.Components = []types.ComponentVersion{{Name: "Podman Engine", Version: "4.9.0"}}
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

	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listening on unix socket %s: %v", path, err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

// forceDefaultDockerUnreachable redirects the newDefaultDockerRuntime seam
// (see detect.go) so the "default Docker socket" resolves to a scratch path
// that is guaranteed to have nothing listening on it, deterministically
// forcing the initial ping in newDockerRuntimeWithPingCandidates to fail —
// regardless of whether this host (or CI runner, which ships a live dockerd
// on ubuntu-latest) has a real reachable default Docker socket. Restored via
// t.Cleanup.
func forceDefaultDockerUnreachable(t *testing.T) {
	t.Helper()
	dead := filepath.Join(shortTempDir(t), "dead-default.sock")
	orig := newDefaultDockerRuntime
	newDefaultDockerRuntime = func(sandbox bool) (*DockerRuntime, error) {
		return NewDockerRuntimeWithHost("unix://"+dead, sandbox)
	}
	t.Cleanup(func() { newDefaultDockerRuntime = orig })
}

// shortTempDir creates a scratch directory directly under /tmp (bypassing
// t.TempDir(), which nests under a per-test path derived from the test name
// — e.g. /var/folders/.../TestMOATRuntimeAutoDetectFallsBackToPodman.../003)
// and, combined with a unix-socket filename, can exceed macOS's ~104-byte
// sun_path limit. Registers cleanup via t.Cleanup.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "moat")
	if err != nil {
		t.Fatalf("creating short scratch dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// TestMOATRuntimeDockerDoesNotFallBackToPodman is the pinning test for the
// genuineDockerSockets()/alternativeDockerSockets() split (see
// NewRuntimeWithOptions's "docker" case and genuineDockerSockets's doc
// comment). Mutation-verified: reverting the "docker" case's
// genuineDockerSockets() argument back to alternativeDockerSockets() passes
// the rest of the suite but must fail this test — with the default Docker
// socket unreachable and a live podman-shaped socket sitting in the podman
// candidate seam, MOAT_RUNTIME=docker must NOT silently land on it.
func TestMOATRuntimeDockerDoesNotFallBackToPodman(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("unix-socket-based fallback probing is unix/darwin-only")
	}

	t.Setenv("DOCKER_HOST", "")
	forceDefaultDockerUnreachable(t)

	// Isolate genuineDockerSockets() (HOME, for the Rancher Desktop
	// candidate) from any real third-party tooling on this machine.
	t.Setenv("HOME", t.TempDir())

	// Plant a live fake podman-shaped socket in the platform-specific podman
	// candidate seam, redirecting the seams so only our fake socket is found.
	var podmanSockPath string
	switch runtime.GOOS {
	case "darwin":
		dir := shortTempDir(t)
		t.Setenv("TMPDIR", dir+"/")
		podmanDir := filepath.Join(dir, "podman")
		if err := os.MkdirAll(podmanDir, 0o755); err != nil {
			t.Fatal(err)
		}
		podmanSockPath = filepath.Join(podmanDir, "podman-machine-default-api.sock")
	case "linux":
		t.Setenv("XDG_RUNTIME_DIR", "")
		dir := shortTempDir(t)
		origRootful := podmanRootfulSocket
		podmanRootfulSocket = filepath.Join(dir, "podman.sock")
		t.Cleanup(func() { podmanRootfulSocket = origRootful })
		podmanSockPath = podmanRootfulSocket
	}
	serveFakeDockerAPIUnixSocket(t, podmanSockPath, true)

	t.Setenv("MOAT_RUNTIME", "docker")
	_, err := NewRuntimeWithOptions(RuntimeOptions{Sandbox: false})
	if err == nil {
		t.Fatal("MOAT_RUNTIME=docker should not silently fall back to a podman socket")
	}
	if !strings.Contains(err.Error(), "Docker runtime requested") {
		t.Errorf("error should mention Docker was requested, got: %v", err)
	}
}

// TestMOATRuntimeDockerWithPodmanDockerHostWarnsAndProceeds pins the
// warn-not-fail behavior of warnIfForcedDockerHostIsPodman (see
// NewRuntimeWithOptions's "docker" case): with MOAT_RUNTIME=docker and
// DOCKER_HOST explicitly pointing at a podman engine, runtime creation must
// SUCCEED — the mismatch is only surfaced as a ui.Warn — unlike
// MOAT_RUNTIME=podman against a non-podman engine, which fails hard
// (TestNewRuntimeWithOptionsPodmanOverrideDockerHostNonPodman). The genuine
// docker-engine subtest is the companion assertion: same setup, no warning.
func TestMOATRuntimeDockerWithPodmanDockerHostWarnsAndProceeds(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("unix-socket-based fake engines are unix/darwin-only")
	}

	tests := []struct {
		name     string
		podman   bool
		wantWarn bool
	}{
		{"podman engine behind DOCKER_HOST warns and proceeds", true, true},
		{"genuine docker engine behind DOCKER_HOST proceeds silently", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sockPath := filepath.Join(shortTempDir(t), "engine.sock")
			serveFakeDockerAPIUnixSocket(t, sockPath, tt.podman)

			t.Setenv("MOAT_RUNTIME", "docker")
			t.Setenv("DOCKER_HOST", "unix://"+sockPath)

			var buf bytes.Buffer
			ui.SetWriter(&buf)
			t.Cleanup(func() { ui.SetWriter(os.Stderr) })

			rt, err := NewRuntimeWithOptions(RuntimeOptions{Sandbox: false})
			if err != nil {
				t.Fatalf("MOAT_RUNTIME=docker with a reachable DOCKER_HOST engine (podman=%v) must succeed, got: %v", tt.podman, err)
			}
			if rt == nil {
				t.Fatal("expected a non-nil runtime")
			}

			warned := strings.Contains(buf.String(), "podman engine")
			if tt.wantWarn && !warned {
				t.Errorf("expected a podman-mismatch warning, ui output was: %q", buf.String())
			}
			if !tt.wantWarn && warned {
				t.Errorf("unexpected podman-mismatch warning for a genuine docker engine: %q", buf.String())
			}
		})
	}
}

// TestMOATRuntimeAutoDetectFallsBackToPodman is the companion to
// TestMOATRuntimeDockerDoesNotFallBackToPodman: with the same dead-default,
// live-podman-socket setup, auto-detection (MOAT_RUNTIME unset) DOES land on
// the podman socket, since podman-or-docker auto-detect is allowed to use
// alternativeDockerSockets (which includes podman candidates).
func TestMOATRuntimeAutoDetectFallsBackToPodman(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("unix-socket-based fallback probing is unix/darwin-only")
	}

	t.Setenv("DOCKER_HOST", "")
	forceDefaultDockerUnreachable(t)

	// Auto-detect tries Apple containers first on darwin/arm64 — take that
	// branch out of the running so this test exercises the Docker fallback
	// path regardless of whether Apple's container CLI is installed here.
	t.Setenv("PATH", "/nonexistent")

	t.Setenv("HOME", t.TempDir())

	var podmanSockPath string
	switch runtime.GOOS {
	case "darwin":
		dir := shortTempDir(t)
		t.Setenv("TMPDIR", dir+"/")
		podmanDir := filepath.Join(dir, "podman")
		if err := os.MkdirAll(podmanDir, 0o755); err != nil {
			t.Fatal(err)
		}
		podmanSockPath = filepath.Join(podmanDir, "podman-machine-default-api.sock")
	case "linux":
		t.Setenv("XDG_RUNTIME_DIR", "")
		dir := shortTempDir(t)
		origRootful := podmanRootfulSocket
		podmanRootfulSocket = filepath.Join(dir, "podman.sock")
		t.Cleanup(func() { podmanRootfulSocket = origRootful })
		podmanSockPath = podmanRootfulSocket
	}
	serveFakeDockerAPIUnixSocket(t, podmanSockPath, true)

	rt, err := NewRuntimeWithOptions(RuntimeOptions{Sandbox: false})
	if err != nil {
		t.Fatalf("auto-detect should land on the fake podman socket: %v", err)
	}
	if rt.Type() != RuntimeDocker {
		t.Errorf("Type() = %v, want %v (podman is served via the Docker runtime)", rt.Type(), RuntimeDocker)
	}
}
