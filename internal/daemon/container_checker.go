package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/majorcontext/moat/internal/log"
)

// CommandContainerChecker checks container liveness by shelling out to
// the container runtime CLI. It tries Docker first, then Apple containers.
type CommandContainerChecker struct {
	// runtime is cached after first successful check ("docker" or "apple").
	runtime string
}

// IsContainerRunning checks if a container is still running.
// Returns (true, nil) when the container is confirmed alive,
// (false, nil) when confirmed dead, and (false, err) when the
// check itself failed (e.g. transient CLI error).
func (c *CommandContainerChecker) IsContainerRunning(ctx context.Context, id string) (bool, error) {
	// Try cached runtime first.
	switch c.runtime {
	case "docker":
		return c.checkDocker(ctx, id)
	case "apple":
		return c.checkApple(ctx, id)
	}

	// No cached runtime — try Docker first (most common).
	alive, err := c.checkDocker(ctx, id)
	if alive {
		c.runtime = "docker"
		return true, nil
	}

	// Try Apple containers.
	alive, appleErr := c.checkApple(ctx, id)
	if alive {
		c.runtime = "apple"
		return true, nil
	}

	// Neither confirmed alive. Return the last error if any.
	if appleErr != nil {
		return false, appleErr
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

// checkDocker checks if a Docker container is running.
func (c *CommandContainerChecker) checkDocker(ctx context.Context, id string) (bool, error) {
	cmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.Running}}", id)
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("docker inspect: %w", err)
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

// checkApple checks if an Apple container is running.
func (c *CommandContainerChecker) checkApple(ctx context.Context, id string) (bool, error) {
	cmd := exec.CommandContext(ctx, "container", "inspect", id)
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("container inspect: %w", err)
	}
	// Apple container inspect returns JSON; a non-empty response with
	// "running" state indicates the container is alive.
	output := string(out)
	return strings.Contains(output, `"running"`) || strings.Contains(output, `"Running"`), nil
}

// NewCommandContainerChecker creates a new container checker that uses CLI commands.
func NewCommandContainerChecker() *CommandContainerChecker {
	log.Debug("created command-based container checker for liveness monitoring")
	return &CommandContainerChecker{}
}
