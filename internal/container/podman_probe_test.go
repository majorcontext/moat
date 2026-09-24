package container

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/ui"
)

// These tests pin podmanSocketCandidates' Linux layout ([rootless, rootful])
// directly, via XDG_RUNTIME_DIR (which the rootless branch reads first) and
// SwapDetectEnv's rootfulSocket field (which the rootful branch always
// uses). On darwin the candidate list is built from a TMPDIR glob instead
// and ignores both, so these tests are Linux-only — matching the
// environment gotest.sh actually runs in (golang:1.25 in Docker).

// TestReachablePodmanEndpointOtherThanExcludesSelf pins the substantive half
// of D2: when the only reachable podman endpoint IS the one the caller
// already queried (e.g. moat auto-detected podman and it's the only engine
// on the host), the probe must report no OTHER engine — there is no
// ambiguity to warn about.
func TestReachablePodmanEndpointOtherThanExcludesSelf(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("candidate layout below is linux-specific")
	}
	dir := shortTempDir(t)
	sockPath := filepath.Join(dir, "podman.sock")
	serveFakeDockerAPIUnixSocket(t, sockPath, true)

	restore := SwapDetectEnv(func(e detectEnviron) detectEnviron {
		e.rootfulSocket = sockPath
		return e
	})
	t.Cleanup(restore)
	// No rootless candidate, so the rootful one (== endpoint) is the only
	// candidate in play.
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(dir, "no-such-runtime-dir"))

	endpoint := "unix://" + sockPath
	got, ok := ReachablePodmanEndpointOtherThan(context.Background(), endpoint)
	if ok {
		t.Errorf("expected no other reachable endpoint (the only candidate equals endpoint), got %q", got)
	}
}

// TestReachablePodmanEndpointOtherThanFindsDistinctEngine is the companion:
// a second, distinct, live podman endpoint alongside the one already queried
// is genuinely ambiguous and must be reported.
func TestReachablePodmanEndpointOtherThanFindsDistinctEngine(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("candidate layout below is linux-specific")
	}
	dir := shortTempDir(t)

	// Rootful candidate: the one already queried.
	queriedPath := filepath.Join(dir, "queried-podman.sock")
	serveFakeDockerAPIUnixSocket(t, queriedPath, true)

	restore := SwapDetectEnv(func(e detectEnviron) detectEnviron {
		e.rootfulSocket = queriedPath
		return e
	})
	t.Cleanup(restore)

	// Rootless candidate: a second, distinct, live podman engine.
	xdgDir := filepath.Join(dir, "xdg")
	rootlessDir := filepath.Join(xdgDir, "podman")
	if err := os.MkdirAll(rootlessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	otherPath := filepath.Join(rootlessDir, "podman.sock")
	serveFakeDockerAPIUnixSocket(t, otherPath, true)
	t.Setenv("XDG_RUNTIME_DIR", xdgDir)

	endpoint := "unix://" + queriedPath
	got, ok := ReachablePodmanEndpointOtherThan(context.Background(), endpoint)
	if !ok {
		t.Fatal("expected a distinct reachable podman endpoint to be found")
	}
	if got != "unix://"+otherPath {
		t.Errorf("got %q, want the distinct candidate unix://%s", got, otherPath)
	}
}

// TestReachablePodmanEndpointOtherThanExcludesSymlinkedSelf pins the
// podman-docker fix (F1): on Fedora/RHEL with the podman-docker package
// installed, /run/docker.sock is a symlink to /run/podman/podman.sock, so
// Manager.Stop's queried endpoint (DaemonHost(), resolved through the
// symlink) is a different *string* from the rootful candidate
// podmanSocketCandidates() finds directly — even though both name the same
// socket, the same engine, and the same container namespace. A string-only
// comparison would fail to exclude the candidate, ping it successfully, and
// report it as a second, distinct, live podman engine — turning a clean
// "container already gone" case into a hard-error false positive. This test
// creates a real socket and a symlink to it at a second path, and queries
// through the symlink, mirroring that layout.
func TestReachablePodmanEndpointOtherThanExcludesSymlinkedSelf(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("candidate layout below is linux-specific")
	}
	dir := shortTempDir(t)

	// The real socket: this is what podmanSocketCandidates() will find
	// directly as the rootful candidate.
	realPath := filepath.Join(dir, "podman.sock")
	serveFakeDockerAPIUnixSocket(t, realPath, true)

	// A symlink to the real socket, standing in for /run/docker.sock ->
	// /run/podman/podman.sock under podman-docker.
	symlinkPath := filepath.Join(dir, "docker.sock")
	if err := os.Symlink(realPath, symlinkPath); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	restore := SwapDetectEnv(func(e detectEnviron) detectEnviron {
		e.rootfulSocket = realPath
		return e
	})
	t.Cleanup(restore)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(dir, "no-such-runtime-dir"))

	// The caller queried through the symlink path, not the real path.
	endpoint := "unix://" + symlinkPath
	got, ok := ReachablePodmanEndpointOtherThan(context.Background(), endpoint)
	if ok {
		t.Errorf("expected the candidate to be excluded as the same socket reached via a symlink, got %q", got)
	}
}

// TestReachablePodmanEndpointOtherThanSkipsNonSocketPaths verifies a
// candidate whose path exists but isn't a socket (e.g. a stale regular
// file) is skipped rather than dialed, mirroring the stat check used
// elsewhere in this package (tryDockerSocketCandidatesVerified).
func TestReachablePodmanEndpointOtherThanSkipsNonSocketPaths(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("candidate layout below is linux-specific")
	}
	dir := shortTempDir(t)
	rootful := filepath.Join(dir, "not-a-socket")
	if err := os.WriteFile(rootful, []byte("not a socket"), 0o644); err != nil {
		t.Fatalf("writing plain file: %v", err)
	}

	restore := SwapDetectEnv(func(e detectEnviron) detectEnviron {
		e.rootfulSocket = rootful
		return e
	})
	t.Cleanup(restore)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(dir, "no-such-runtime-dir"))

	got, ok := ReachablePodmanEndpointOtherThan(context.Background(), "unix://never-queried")
	if ok {
		t.Errorf("a non-socket path must be skipped, got %q", got)
	}
}

// TestReachablePodmanEndpointOtherThanNoneReachable verifies the "podman
// absent entirely" case: no candidate stats as a socket, so the probe
// reports nothing reachable (this is the common Docker-only host, and must
// stay cheap and side-effect-free).
func TestReachablePodmanEndpointOtherThanNoneReachable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("candidate layout below is linux-specific")
	}
	dir := shortTempDir(t)
	restore := SwapDetectEnv(func(e detectEnviron) detectEnviron {
		e.rootfulSocket = filepath.Join(dir, "no-rootful-socket-here")
		return e
	})
	t.Cleanup(restore)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(dir, "no-such-runtime-dir"))

	got, ok := ReachablePodmanEndpointOtherThan(context.Background(), "unix://never-queried")
	if ok {
		t.Errorf("expected no reachable endpoint on a host with no podman socket, got %q", got)
	}
}

// TestIsReachablePodmanEmitsNoUIOutput pins Part A's regression: this probe
// runs on Manager.Stop's not-found path, where no container is being created
// or run at all. isReachablePodman must build a bare Docker API client
// rather than a full DockerRuntime — going through
// NewDockerRuntimeWithHost(host, false) would route through
// newDockerRuntimeFromClient's sandbox=false handling, which on Linux
// unconditionally prints "Running without gVisor sandbox. Container
// isolation is reduced." before this function ever pings anything. That
// would be a false security alarm from a command that creates and runs no
// container whatsoever — moat must never cry wolf here.
func TestIsReachablePodmanEmitsNoUIOutput(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the gVisor warning this guards against only fires on linux (see newDockerRuntimeFromClient)")
	}

	var buf bytes.Buffer
	ui.SetWriter(&buf)
	t.Cleanup(func() { ui.SetWriter(os.Stderr) })

	dir := shortTempDir(t)
	sockPath := filepath.Join(dir, "podman.sock")
	serveFakeDockerAPIUnixSocket(t, sockPath, true)

	if !isReachablePodman(context.Background(), "unix://"+sockPath) {
		t.Fatal("expected the fake podman socket to be recognized as reachable")
	}
	if got := buf.String(); got != "" {
		t.Errorf("isReachablePodman must not print anything to the UI, got %q", got)
	}
}

// TestReachablePodmanEndpointOtherThanWedgedCandidateDoesNotStarveOthers
// pins the per-candidate sub-timeout: a first candidate (rootless, tried
// first) that accepts the connection but never answers must not be allowed
// to consume the whole ~3s overall deadline and starve a second, good
// candidate (rootful). The elapsed-time bound also stands in for "every
// constructed runtime is closed promptly" — a client left open against the
// wedged candidate would tend to push this well past the ~1s sub-timeout.
func TestReachablePodmanEndpointOtherThanWedgedCandidateDoesNotStarveOthers(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("candidate layout below is linux-specific")
	}
	dir := shortTempDir(t)

	xdgDir := filepath.Join(dir, "xdg")
	rootlessDir := filepath.Join(xdgDir, "podman")
	if err := os.MkdirAll(rootlessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wedgedPath := filepath.Join(rootlessDir, "podman.sock")
	wedgedLn, err := net.Listen("unix", wedgedPath)
	if err != nil {
		t.Fatalf("listen (wedged): %v", err)
	}
	defer wedgedLn.Close()
	// Accept connections but never write a response, simulating a socket
	// that answers at the TCP level but hangs at the HTTP/API level.
	go func() {
		for {
			conn, err := wedgedLn.Accept()
			if err != nil {
				return
			}
			_ = conn // held open, deliberately never responded to
		}
	}()
	t.Setenv("XDG_RUNTIME_DIR", xdgDir)

	goodPath := filepath.Join(dir, "good-podman.sock")
	serveFakeDockerAPIUnixSocket(t, goodPath, true)

	restore := SwapDetectEnv(func(e detectEnviron) detectEnviron {
		e.rootfulSocket = goodPath
		return e
	})
	t.Cleanup(restore)

	start := time.Now()
	got, ok := ReachablePodmanEndpointOtherThan(context.Background(), "unix://never-queried")
	elapsed := time.Since(start)

	if !ok {
		t.Fatal("expected the good candidate to be found despite the wedged one preceding it")
	}
	if got != "unix://"+goodPath {
		t.Errorf("got %q, want the good candidate unix://%s", got, goodPath)
	}
	// Generous margin above the ~1s per-candidate deadline (not the ~3s
	// overall one): if the wedged candidate were consuming the whole
	// budget, this would land much closer to probeOverallDeadline.
	if elapsed > 2*time.Second {
		t.Errorf("probe took %s; the wedged first candidate should not have consumed most of the overall deadline", elapsed)
	}
}
