package daemon

import (
	"context"
	"time"

	"github.com/majorcontext/moat/internal/log"
)

// ContainerChecker checks if a container is still running.
type ContainerChecker interface {
	IsContainerRunning(ctx context.Context, id string) bool
}

// LivenessChecker periodically checks container liveness and cleans up dead runs.
type LivenessChecker struct {
	registry  *Registry
	checker   ContainerChecker
	interval  time.Duration
	onCleanup func(token string)
}

// NewLivenessChecker creates a new liveness checker with 30-second default interval.
func NewLivenessChecker(registry *Registry, checker ContainerChecker) *LivenessChecker {
	return &LivenessChecker{
		registry: registry,
		checker:  checker,
		interval: 30 * time.Second,
	}
}

// SetOnCleanup sets a callback invoked when a run is cleaned up.
func (lc *LivenessChecker) SetOnCleanup(fn func(token string)) {
	lc.onCleanup = fn
}

// CheckOnce performs a single liveness check for all registered runs.
func (lc *LivenessChecker) CheckOnce(ctx context.Context) {
	for _, rc := range lc.registry.List() {
		// Skip runs that haven't completed phase 2 registration
		if rc.ContainerID == "" {
			continue
		}
		// Per-check timeout prevents a hung container runtime from blocking
		// all liveness checks (and by extension the idle shutdown timer).
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		alive := lc.checker.IsContainerRunning(checkCtx, rc.ContainerID)
		cancel()
		if !alive {
			log.Info("container no longer running, cleaning up",
				"run_id", rc.RunID,
				"container_id", rc.ContainerID)
			lc.registry.Unregister(rc.AuthToken)
			if lc.onCleanup != nil {
				lc.onCleanup(rc.AuthToken)
			}
		}
	}
}

// Run starts the periodic liveness check loop. Blocks until ctx is canceled.
func (lc *LivenessChecker) Run(ctx context.Context) {
	ticker := time.NewTicker(lc.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			lc.CheckOnce(ctx)
		}
	}
}
