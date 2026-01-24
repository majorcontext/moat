package container

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/andybons/moat/internal/log"
)

// NewRuntime creates a new container runtime, auto-detecting the best available option.
// On macOS with Apple Silicon, it prefers Apple's container tool if available,
// falling back to Docker otherwise.
func NewRuntime() (Runtime, error) {
	// On macOS with Apple Silicon, prefer Apple's container tool
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		if rt, reason := tryAppleRuntime(); rt != nil {
			return rt, nil
		} else if reason != "" {
			log.Info(reason)
		}
	}

	// Fall back to Docker
	rt, err := NewDockerRuntime()
	if err != nil {
		return nil, fmt.Errorf("no container runtime available: Docker error: %w", err)
	}

	// Verify Docker is accessible
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rt.Ping(ctx); err != nil {
		return nil, fmt.Errorf("no container runtime available: %w", err)
	}

	log.Info("using Docker runtime")
	return rt, nil
}

// tryAppleRuntime attempts to create an Apple runtime.
// Returns (runtime, "") on success, (nil, reason) on failure with reason message,
// or (nil, "") if Apple container is not available.
func tryAppleRuntime() (Runtime, string) {
	if !appleContainerAvailable() {
		log.Debug("Apple container CLI not found, using Docker")
		return nil, ""
	}

	rt, err := NewAppleRuntime()
	if err != nil {
		return nil, fmt.Sprintf("Apple container not available, falling back to Docker: %v", err)
	}

	// Verify it's actually working
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if pingErr := rt.Ping(ctx); pingErr != nil {
		// Try to start the Apple container system
		log.Info("Apple container system not running, attempting to start...")
		if startErr := startAppleContainerSystem(); startErr != nil {
			return nil, fmt.Sprintf("Apple container system failed to start, falling back to Docker: %v", startErr)
		}

		// Verify it's now working
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()

		if pingErr2 := rt.Ping(ctx2); pingErr2 != nil {
			return nil, fmt.Sprintf("Apple container system started but not accessible, falling back to Docker: %v", pingErr2)
		}
	}

	log.Info("using Apple container runtime")
	return rt, ""
}

// startAppleContainerSystem starts the Apple container system using 'container system start'.
func startAppleContainerSystem() error {
	fmt.Println("Starting Apple container system...")

	// Use a single timeout for the entire operation (start + readiness check)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "container", "system", "start")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("container system start: %w", err)
	}

	// Wait for the system to be fully ready, respecting the parent context timeout
	fmt.Println("Waiting for Apple container system to be ready...")
	const maxAttempts = 30
	for i := 0; i < maxAttempts; i++ {
		// Check if parent context is done
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for Apple container system to be ready: %w", ctx.Err())
		default:
		}

		checkCtx, checkCancel := context.WithTimeout(ctx, 2*time.Second)
		checkCmd := exec.CommandContext(checkCtx, "container", "list", "--quiet")
		if err := checkCmd.Run(); err == nil {
			checkCancel()
			return nil
		}
		checkCancel()

		// Log progress every 10 seconds
		if (i+1)%10 == 0 {
			fmt.Printf("Still waiting for Apple container system... (%d/%d attempts)\n", i+1, maxAttempts)
		}

		// Sleep with context awareness
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for Apple container system to be ready: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}

	return fmt.Errorf("system started but did not become ready within 30 seconds")
}

// appleContainerAvailable checks if Apple's container CLI is installed.
func appleContainerAvailable() bool {
	_, err := exec.LookPath("container")
	return err == nil
}

// IsAppleSilicon returns true if running on Apple Silicon.
func IsAppleSilicon() bool {
	return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
}
