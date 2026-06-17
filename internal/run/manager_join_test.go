package run

import (
	"context"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/container"
)

func TestManagerExecInteractive_RunNotFound(t *testing.T) {
	m := &Manager{runs: map[string]*Run{}}
	err := m.ExecInteractive(context.Background(), "run_missing", []string{"claude"}, container.ExecOptions{})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v, want a 'not found' error", err)
	}
}

func TestManagerExecInteractive_NotRunning(t *testing.T) {
	r := &Run{ID: "run_stopped", State: StateStopped}
	m := &Manager{runs: map[string]*Run{"run_stopped": r}}
	err := m.ExecInteractive(context.Background(), "run_stopped", []string{"claude"}, container.ExecOptions{})
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("got %v, want a 'not running' error", err)
	}
}

// TestManagerExecInteractive_RuntimeResolveError verifies that ExecInteractive
// returns the "resolving runtime" error when the run's Runtime field names a
// type that is not available in the pool.
func TestManagerExecInteractive_RuntimeResolveError(t *testing.T) {
	// newEdgeCaseManager creates a pool whose only runtime is the flexibleRuntime
	// (type = RuntimeDocker). A run whose Runtime field is set to "apple" requests
	// a different type — one that is not in the pool — which triggers the
	// runtimeForRun error path.
	rt := &flexibleRuntime{done: make(chan struct{})}
	m := newEdgeCaseManager(t, rt)

	r := &Run{
		ID:          "run_rt_resolve",
		Name:        "rt-resolve",
		ContainerID: "ctr-rt",
		State:       StateRunning,
		Runtime:     string(container.RuntimeApple), // pool only has Docker
		exitCh:      make(chan struct{}),
	}
	m.mu.Lock()
	m.runs[r.ID] = r
	m.mu.Unlock()

	err := m.ExecInteractive(context.Background(), r.ID, []string{"claude"}, container.ExecOptions{})
	if err == nil {
		t.Fatal("expected error when runtime cannot be resolved, got nil")
	}
	if !strings.Contains(err.Error(), "resolving runtime") {
		t.Fatalf("expected 'resolving runtime' in error, got: %v", err)
	}
}
