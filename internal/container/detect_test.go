package container

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
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

	t.Setenv("XDG_RUNTIME_DIR", "")
	candidates := podmanSocketCandidates()

	if len(candidates) != 1 {
		t.Fatalf("expected only the rootful candidate when XDG_RUNTIME_DIR is unset, got %+v", candidates)
	}
	if candidates[0].path != "/run/podman/podman.sock" {
		t.Errorf("path = %q, want %q", candidates[0].path, "/run/podman/podman.sock")
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

	rt := tryAlternativeDockerSockets(false)
	if rt != nil {
		t.Error("expected nil when no alternative sockets exist")
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
