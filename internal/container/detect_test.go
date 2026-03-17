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

func TestAlternativeDockerSockets(t *testing.T) {
	candidates := alternativeDockerSockets()
	if len(candidates) == 0 {
		t.Fatal("expected at least one alternative socket candidate")
	}

	// Verify known tools are included.
	names := make(map[string]bool)
	for _, c := range candidates {
		names[c.name] = true
	}
	if !names["Rancher Desktop"] {
		t.Error("missing expected candidate \"Rancher Desktop\"")
	}
}

func TestAlternativeDockerSocketPaths(t *testing.T) {
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
	// With HOME pointing to an empty dir, no alternative sockets should exist.
	t.Setenv("HOME", t.TempDir())

	rt := tryAlternativeDockerSockets(false)
	if rt != nil {
		t.Error("expected nil when no alternative sockets exist")
	}
}

func TestTryAlternativeDockerSocketsCleansUpOnRuntimeFailure(t *testing.T) {
	// Create a real Unix socket in a temp dir so os.Lstat + ModeSocket check passes.
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "docker.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("creating test socket: %v", err)
	}
	defer ln.Close()

	// Point HOME at the temp dir so alternativeDockerSockets returns our socket.
	// The Rancher Desktop path is ~/.rd/docker.sock, so create the subdirectory.
	rdDir := filepath.Join(dir, ".rd")
	if err := os.Mkdir(rdDir, 0o755); err != nil {
		t.Fatalf("creating .rd dir: %v", err)
	}
	rdSock := filepath.Join(rdDir, "docker.sock")
	ln2, err := net.Listen("unix", rdSock)
	if err != nil {
		t.Fatalf("creating .rd/docker.sock: %v", err)
	}
	defer ln2.Close()

	t.Setenv("HOME", dir)
	t.Setenv("DOCKER_HOST", "") // ensure it starts unset

	// The socket exists and is a real socket, but it's not a Docker daemon,
	// so Ping will fail and DOCKER_HOST should be cleared.
	rt := tryAlternativeDockerSockets(false)
	if rt != nil {
		t.Error("expected nil runtime when ping fails")
	}
	if got := os.Getenv("DOCKER_HOST"); got != "" {
		t.Errorf("DOCKER_HOST should be cleared on failure, got %q", got)
	}
}
