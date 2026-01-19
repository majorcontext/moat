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
		return nil, fmt.Sprintf("Apple container system not running, falling back to Docker: %v", pingErr)
	}

	log.Info("using Apple container runtime")
	return rt, ""
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
