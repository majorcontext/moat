package container

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/docker/docker/client"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/ui"
)

// RuntimeOptions configures runtime creation.
type RuntimeOptions struct {
	// Sandbox enables gVisor sandboxing for Docker containers.
	// When true (default), requires gVisor and fails if unavailable.
	// When false, uses runc with reduced isolation.
	Sandbox bool
}

// DefaultRuntimeOptions returns the default runtime options.
// On Linux, defaults to sandbox=true (requires gVisor).
// On macOS and Windows, defaults to sandbox=false (gVisor unavailable in Docker Desktop).
func DefaultRuntimeOptions() RuntimeOptions {
	// gVisor is only available on Linux
	// Docker Desktop on macOS and Windows does not support gVisor
	sandbox := runtime.GOOS == "linux"
	if os.Getenv("MOAT_NO_SANDBOX") == "1" {
		sandbox = false
	}
	return RuntimeOptions{Sandbox: sandbox}
}

// NewRuntimeWithOptions creates a new container runtime with the given options.
func NewRuntimeWithOptions(opts RuntimeOptions) (Runtime, error) {
	// Check for explicit runtime override
	if override := os.Getenv("MOAT_RUNTIME"); override != "" {
		switch strings.ToLower(override) {
		case "docker":
			log.Debug("using Docker runtime (MOAT_RUNTIME=docker)")
			rt, err := newDockerRuntimeWithPing(opts.Sandbox)
			if err != nil {
				hint := "Set MOAT_RUNTIME=apple, use --runtime apple, or remove 'runtime: docker' from agent.yaml to use auto-detection."
				return nil, fmt.Errorf("Docker runtime requested (via MOAT_RUNTIME or agent.yaml) but not available: %w\n\n%s", err, hint)
			}
			return rt, nil
		case "apple":
			log.Debug("using Apple container runtime (MOAT_RUNTIME=apple)")
			rt, reason := tryAppleRuntime()
			if rt != nil {
				return rt, nil
			}
			return nil, fmt.Errorf("Apple container runtime not available: %s\n\nTo start the container system manually:\n  container system start", reason)
		default:
			return nil, fmt.Errorf("unknown MOAT_RUNTIME value %q (use 'docker' or 'apple')", override)
		}
	}

	// On macOS with Apple Silicon, prefer Apple's container tool
	var appleReason string
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		var rt Runtime
		rt, appleReason = tryAppleRuntime()
		if rt != nil {
			return rt, nil
		}
		if appleReason != "" {
			log.Debug(appleReason)
		}
	}

	// Fall back to Docker
	rt, err := newDockerRuntimeWithPing(opts.Sandbox)
	if err != nil {
		if appleReason != "" {
			return nil, fmt.Errorf("no container runtime available:\n  Apple containers: %s\n  Docker: %w\n\nTo start Apple containers manually:\n  container system start\n\nTo force a specific runtime:\n  moat run --runtime apple\n  moat run --runtime docker", appleReason, err)
		}
		return nil, fmt.Errorf("no container runtime available: %w", err)
	}
	return rt, nil
}

// NewRuntime creates a new container runtime, auto-detecting the best available option.
// On macOS with Apple Silicon, it prefers Apple's container tool if available,
// falling back to Docker otherwise. Docker containers use gVisor by default.
//
// The MOAT_RUNTIME environment variable can override auto-detection:
//   - MOAT_RUNTIME=docker: force Docker runtime
//   - MOAT_RUNTIME=apple: force Apple container runtime
func NewRuntime() (Runtime, error) {
	return NewRuntimeWithOptions(DefaultRuntimeOptions())
}

// newDockerRuntimeWithPing creates a Docker runtime and verifies it's accessible.
func newDockerRuntimeWithPing(sandbox bool) (Runtime, error) {
	rt, err := NewDockerRuntime(sandbox)
	if err != nil {
		return nil, fmt.Errorf("Docker runtime error: %w", err)
	}

	// Verify Docker is accessible
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rt.Ping(ctx); err != nil {
		return nil, err
	}

	runtimeName := "Docker"
	if sandbox {
		runtimeName = "Docker+gVisor"
	} else if runtime.GOOS != "linux" {
		// On macOS/Windows, gVisor is unavailable in Docker Desktop
		log.Debug("using Docker runtime (gVisor unavailable on " + runtime.GOOS + ")")
		return rt, nil
	}
	log.Debug("using " + runtimeName + " runtime")
	return rt, nil
}

// tryAppleRuntime attempts to create and verify an Apple runtime.
// Returns (runtime, "") on success, or (nil, reason) on failure.
func tryAppleRuntime() (Runtime, string) {
	if !appleContainerAvailable() {
		return nil, "'container' CLI not found in PATH (requires macOS 26+ with containerization framework)"
	}

	rt, err := NewAppleRuntime()
	if err != nil {
		return nil, fmt.Sprintf("failed to initialize: %v", err)
	}

	// Verify the system is running
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if pingErr := rt.Ping(ctx); pingErr != nil {
		// Try to auto-start the Apple container system.
		// startAppleContainerSystem polls for readiness internally, so if it
		// returns nil, the system is verified as up and we don't need to ping again.
		log.Debug("Apple container system not running, attempting to start...", "error", pingErr)
		if startErr := startAppleContainerSystem(); startErr != nil {
			return nil, fmt.Sprintf("system not running and failed to auto-start: %v", startErr)
		}
	}

	log.Debug("using Apple container runtime")
	return rt, ""
}

// startAppleContainerSystem starts the Apple container system using 'container system start'.
func startAppleContainerSystem() error {
	ui.Info("Starting Apple container system...")

	// Use a single timeout for the entire operation (start + readiness check)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "container", "system", "start")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("'container system start' failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	// Wait for the system to be fully ready, respecting the parent context timeout
	ui.Info("Waiting for Apple container system to be ready...")
	const maxAttempts = 30
	for i := 0; i < maxAttempts; i++ {
		// Check if parent context is done
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for system to be ready: %w", ctx.Err())
		default:
		}

		checkCtx, checkCancel := context.WithTimeout(ctx, 2*time.Second)
		checkCmd := exec.CommandContext(checkCtx, "container", "list", "--quiet")
		var checkStderr strings.Builder
		checkCmd.Stderr = &checkStderr
		checkErr := checkCmd.Run()
		checkCancel()
		if checkErr == nil {
			return nil
		}
		log.Debug("readiness check failed", "attempt", i+1, "error", checkErr, "stderr", strings.TrimSpace(checkStderr.String()))

		// Log progress every 10 seconds
		if (i+1)%10 == 0 {
			ui.Infof("Still waiting for Apple container system... (%d/%d attempts)", i+1, maxAttempts)
		}

		// Sleep with context awareness
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for system to be ready: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}

	return fmt.Errorf("system started but did not become ready within 30 seconds")
}

// appleContainerAvailable checks if Apple's container CLI is installed.
// Requires macOS 26+ with the containerization framework.
func appleContainerAvailable() bool {
	_, err := exec.LookPath("container")
	return err == nil
}

// IsAppleSilicon returns true if running on Apple Silicon.
func IsAppleSilicon() bool {
	return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
}

// GVisorAvailable checks if runsc is configured as a Docker runtime.
// Returns true if Docker reports "runsc" in its available runtimes.
//
// Deprecated: This function creates a new Docker client on each call, which is
// inefficient. Use DockerRuntime.gvisorAvailable() instead, which caches the
// result after the first check. This function is kept for backward compatibility
// with existing tests.
func GVisorAvailable(ctx context.Context) bool {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return false
	}
	defer cli.Close()

	info, err := cli.Info(ctx)
	if err != nil {
		return false
	}

	for name := range info.Runtimes {
		if name == "runsc" {
			return true
		}
	}
	return false
}
