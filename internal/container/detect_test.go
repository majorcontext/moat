package container

import (
	"context"
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

func TestIsKernelNotConfiguredError(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"kernel not configured", true},
		{"default kernel", true},
		{"some other error", false},
		{"", false},
		{"Error: kernel not configured for this architecture", true},
	}
	for _, tt := range tests {
		if got := isKernelNotConfiguredError(tt.msg); got != tt.want {
			t.Errorf("isKernelNotConfiguredError(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}
