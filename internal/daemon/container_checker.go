package daemon

import (
	"context"
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
func (c *CommandContainerChecker) IsContainerRunning(ctx context.Context, id string) bool {
	// Try cached runtime first.
	switch c.runtime {
	case "docker":
		return c.checkDocker(ctx, id)
	case "apple":
		return c.checkApple(ctx, id)
	}

	// No cached runtime — try Docker first (most common).
	if c.checkDocker(ctx, id) {
		c.runtime = "docker"
		return true
	}

	// Try Apple containers.
	if c.checkApple(ctx, id) {
		c.runtime = "apple"
		return true
	}

	return false
}

// checkDocker checks if a Docker container is running.
func (c *CommandContainerChecker) checkDocker(ctx context.Context, id string) bool {
	cmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.Running}}", id)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// checkApple checks if an Apple container is running.
func (c *CommandContainerChecker) checkApple(ctx context.Context, id string) bool {
	cmd := exec.CommandContext(ctx, "container", "inspect", id)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	// Apple container inspect returns JSON; a non-empty response with
	// "running" state indicates the container is alive.
	output := string(out)
	return strings.Contains(output, `"running"`) || strings.Contains(output, `"Running"`)
}

// NewCommandContainerChecker creates a new container checker that uses CLI commands.
func NewCommandContainerChecker() *CommandContainerChecker {
	log.Debug("created command-based container checker for liveness monitoring")
	return &CommandContainerChecker{}
}
