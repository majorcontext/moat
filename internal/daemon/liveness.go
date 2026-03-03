package daemon

import (
	"context"
	"time"

	"github.com/majorcontext/moat/internal/log"
)

// ContainerChecker checks if a container is still running.
// Returns (true, nil) when confirmed alive, (false, nil) when confirmed dead,
// and (false, err) when the check failed (transient error).
type ContainerChecker interface {
	IsContainerRunning(ctx context.Context, id string) (alive bool, err error)
}

// defaultMaxFailures is the number of consecutive check errors required
// before treating a container as dead. A single transient docker-inspect
// failure will no longer cause immediate cleanup.
const defaultMaxFailures = 3

// LivenessChecker periodically checks container liveness and cleans up dead runs.
type LivenessChecker struct {
	registry    *Registry
	checker     ContainerChecker
	interval    time.Duration
	onCleanup   func(token, runID string)
	onEmpty     func()         // called when registry becomes empty after cleanup
	failCounts  map[string]int // keyed by containerID (not token or runID)
	maxFailures int
}

// NewLivenessChecker creates a new liveness checker with 30-second default interval.
func NewLivenessChecker(registry *Registry, checker ContainerChecker) *LivenessChecker {
	return &LivenessChecker{
		registry:    registry,
		checker:     checker,
		interval:    30 * time.Second,
		failCounts:  make(map[string]int),
		maxFailures: defaultMaxFailures,
	}
}

// SetOnCleanup sets a callback invoked when a run is cleaned up.
// The callback receives both the auth token and run ID.
func (lc *LivenessChecker) SetOnCleanup(fn func(token, runID string)) {
	lc.onCleanup = fn
}

// SetOnEmpty sets a callback invoked when the registry becomes empty after cleanup.
func (lc *LivenessChecker) SetOnEmpty(fn func()) {
	lc.onEmpty = fn
}

// CheckOnce performs a single liveness check for all registered runs.
func (lc *LivenessChecker) CheckOnce(ctx context.Context) {
	for _, rc := range lc.registry.List() {
		// Skip runs that haven't completed phase 2 registration
		containerID := rc.GetContainerID()
		if containerID == "" {
			continue
		}
		// Per-check timeout prevents a hung container runtime from blocking
		// all liveness checks (and by extension the idle shutdown timer).
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		alive, checkErr := lc.checker.IsContainerRunning(checkCtx, containerID)
		cancel()

		switch {
		case alive:
			// Container confirmed alive — reset any failure count.
			delete(lc.failCounts, containerID)

		case checkErr == nil:
			// Container confirmed dead (not alive, no error) — remove immediately.
			delete(lc.failCounts, containerID)
			log.Info("container no longer running, cleaning up",
				"run_id", rc.RunID,
				"container_id", containerID)
			lc.removeRun(rc)

		default:
			// Check failed (transient error) — increment failure count.
			lc.failCounts[containerID]++
			count := lc.failCounts[containerID]
			log.Warn("container liveness check failed",
				"run_id", rc.RunID,
				"container_id", containerID,
				"error", checkErr,
				"fail_count", count,
				"max_failures", lc.maxFailures)
			if count >= lc.maxFailures {
				delete(lc.failCounts, containerID)
				log.Info("container liveness check exceeded failure threshold, cleaning up",
					"run_id", rc.RunID,
					"container_id", containerID)
				lc.removeRun(rc)
			}
		}
	}
}

// removeRun cancels refresh, unregisters the run, and fires callbacks.
func (lc *LivenessChecker) removeRun(rc *RunContext) {
	rc.CancelRefresh()
	lc.registry.Unregister(rc.AuthToken)
	if lc.onCleanup != nil {
		lc.onCleanup(rc.AuthToken, rc.RunID)
	}
	if lc.onEmpty != nil && lc.registry.Count() == 0 {
		lc.onEmpty()
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
