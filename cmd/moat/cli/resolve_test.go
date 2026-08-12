package cli

import (
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/run"
)

func TestFilterRunning(t *testing.T) {
	running := &run.Run{ID: "run_a", State: run.StateRunning}
	stopped := &run.Run{ID: "run_b", State: run.StateStopped}

	got := filterRunning([]*run.Run{running, stopped})
	if len(got) != 1 || got[0].ID != "run_a" {
		t.Errorf("filterRunning should keep only running runs; got %v", got)
	}

	// Companion: an all-stopped set filters to empty rather than erroring.
	if got := filterRunning([]*run.Run{stopped}); len(got) != 0 {
		t.Errorf("all-stopped set should filter to empty; got %v", got)
	}
}

func TestResolveRunningRunArgReportsStoppedState(t *testing.T) {
	// A named run that exists but is stopped keeps the specific error rather
	// than degrading to "no run found" — the specific message tells the user
	// what to do.
	stopped := &run.Run{ID: "run_b", Name: "solo", State: run.StateStopped}
	_, _, err := resolveRunningFrom([]*run.Run{stopped}, "solo")
	if err == nil {
		t.Fatal("expected an error for a stopped run")
	}
	if !strings.Contains(err.Error(), "not running") || !strings.Contains(err.Error(), "stopped") {
		t.Errorf("error should name the state; got %q", err)
	}
}

func TestResolveRunningFromSingleRunning(t *testing.T) {
	// Companion to the multiple-running and stopped-state cases: a match set
	// that resolves to exactly one running run returns it directly with no
	// error and no candidate list.
	stopped := &run.Run{ID: "run_a", Name: "solo", State: run.StateStopped}
	running := &run.Run{ID: "run_b", Name: "solo", State: run.StateRunning}

	single, candidates, err := resolveRunningFrom([]*run.Run{stopped, running}, "solo")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if single == nil || single.ID != "run_b" {
		t.Errorf("expected single match run_b, got %v", single)
	}
	if candidates != nil {
		t.Errorf("expected no candidates, got %v", candidates)
	}
}

func TestResolveRunningFromMultipleRunning(t *testing.T) {
	a := &run.Run{ID: "run_a", Name: "moat-dev", State: run.StateRunning}
	b := &run.Run{ID: "run_b", Name: "moat-dev", State: run.StateRunning}

	single, candidates, err := resolveRunningFrom([]*run.Run{a, b}, "moat-dev")
	if err != nil {
		t.Fatalf("two running runs should not error here; got %v", err)
	}
	if single != nil {
		t.Errorf("expected no single match, got %v", single)
	}
	if len(candidates) != 2 {
		t.Errorf("expected 2 candidates for the picker, got %d", len(candidates))
	}
}
