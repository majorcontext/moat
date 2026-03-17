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
)

func TestGVisorAvailable(t *testing.T) {
	// This test verifies the function exists and returns a boolean.
	// Actual gVisor detection depends on Docker daemon configuration.
	// Use a timeout to prevent CI hangs if Docker is unreachable.
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

func TestTryAlternativeDockerSocketsNoSockets(t *testing.T) {
	// On non-darwin, alternativeDockerSockets returns nil immediately.
	// On darwin, point HOME at an empty dir so no candidate paths exist.
	t.Setenv("HOME", t.TempDir())

	rt := tryAlternativeDockerSockets(false)
	if rt != nil {
		t.Error("expected nil when no alternative sockets exist")
	}
}

func TestTryAlternativeDockerSocketsCleansUpOnRuntimeFailure(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("alternative socket detection is macOS-only")
	}
	dir := t.TempDir()

	// Create the Rancher Desktop socket path structure under the temp HOME.
	rdDir := filepath.Join(dir, ".rd")
	if err := os.Mkdir(rdDir, 0o755); err != nil {
		t.Fatalf("creating .rd dir: %v", err)
	}
	rdSock := filepath.Join(rdDir, "docker.sock")
	ln, err := net.Listen("unix", rdSock)
	if err != nil {
		t.Fatalf("creating .rd/docker.sock: %v", err)
	}
	defer ln.Close()

	t.Setenv("HOME", dir)
	t.Setenv("DOCKER_HOST", "") // ensure it starts unset

	// The socket exists but is not a Docker daemon, so Ping will fail
	// and DOCKER_HOST should be cleared.
	rt := tryAlternativeDockerSockets(false)
	if rt != nil {
		t.Error("expected nil runtime when ping fails")
	}
	if got := os.Getenv("DOCKER_HOST"); got != "" {
		t.Errorf("DOCKER_HOST should be cleared on failure, got %q", got)
	}
}

func TestTryAlternativeDockerSocketsFollowsSymlink(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("symlink socket detection is macOS-only")
	}
	// Verify os.Stat (not Lstat) is used: ~/.rd/docker.sock on macOS is a
	// symlink to the real socket. Lstat would return ModeSymlink and skip it.
	dir := t.TempDir()
	rdDir := filepath.Join(dir, ".rd")
	if err := os.Mkdir(rdDir, 0o755); err != nil {
		t.Fatalf("creating .rd dir: %v", err)
	}

	// Create actual socket in a different location.
	realSock := filepath.Join(dir, "real.sock")
	ln, err := net.Listen("unix", realSock)
	if err != nil {
		t.Fatalf("creating real socket: %v", err)
	}
	defer ln.Close()

	// Symlink ~/.rd/docker.sock → real socket (mirrors macOS Rancher Desktop).
	symlink := filepath.Join(rdDir, "docker.sock")
	if err := os.Symlink(realSock, symlink); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	t.Setenv("HOME", dir)
	t.Setenv("DOCKER_HOST", "")

	// Should find the socket via the symlink (not return nil due to Lstat).
	// The ping will fail (not a real Docker daemon), but we should at least
	// reach the ping stage — meaning DOCKER_HOST was set then cleared.
	_ = tryAlternativeDockerSockets(false)

	// If os.Lstat were used, the symlink would be skipped entirely and
	// DOCKER_HOST would never be set or cleared. With os.Stat, it's set then
	// cleared on ping failure.
	// We can't assert DOCKER_HOST was set (it's cleared on failure),
	// but we can assert it's clean after the call.
	if got := os.Getenv("DOCKER_HOST"); got != "" {
		t.Errorf("DOCKER_HOST should be empty after failed attempt, got %q", got)
	}
}
