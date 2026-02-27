package daemon

import (
	"context"
	"testing"
)

type mockContainerChecker struct {
	alive map[string]bool
}

func (m *mockContainerChecker) IsContainerRunning(_ context.Context, id string) bool {
	return m.alive[id]
}

func TestLivenessChecker_RemovesDeadContainers(t *testing.T) {
	reg := NewRegistry()

	rc1 := NewRunContext("run_1")
	rc1.ContainerID = "alive_container"
	reg.Register(rc1)

	rc2 := NewRunContext("run_2")
	rc2.ContainerID = "dead_container"
	reg.Register(rc2)

	checker := &mockContainerChecker{
		alive: map[string]bool{
			"alive_container": true,
			"dead_container":  false,
		},
	}

	lc := NewLivenessChecker(reg, checker)
	lc.CheckOnce(context.Background())

	if reg.Count() != 1 {
		t.Errorf("expected 1 run after cleanup, got %d", reg.Count())
	}

	runs := reg.List()
	if runs[0].RunID != "run_1" {
		t.Errorf("expected run_1 to survive, got %s", runs[0].RunID)
	}
}

func TestLivenessChecker_SkipsRunsWithoutContainerID(t *testing.T) {
	reg := NewRegistry()
	rc := NewRunContext("run_pending")
	// No ContainerID set (phase 1 only)
	reg.Register(rc)

	checker := &mockContainerChecker{alive: map[string]bool{}}

	lc := NewLivenessChecker(reg, checker)
	lc.CheckOnce(context.Background())

	if reg.Count() != 1 {
		t.Error("runs without ContainerID should not be cleaned up")
	}
}

func TestLivenessChecker_CallsOnCleanup(t *testing.T) {
	reg := NewRegistry()
	rc := NewRunContext("run_dead")
	rc.ContainerID = "dead"
	token := reg.Register(rc)

	checker := &mockContainerChecker{alive: map[string]bool{"dead": false}}

	var cleanedUp string
	lc := NewLivenessChecker(reg, checker)
	lc.SetOnCleanup(func(t, _ string) { cleanedUp = t })

	lc.CheckOnce(context.Background())

	if cleanedUp != token {
		t.Errorf("expected onCleanup called with %s, got %s", token, cleanedUp)
	}
}

func TestLivenessChecker_AllAlive(t *testing.T) {
	reg := NewRegistry()
	rc1 := NewRunContext("run_1")
	rc1.ContainerID = "c1"
	reg.Register(rc1)

	rc2 := NewRunContext("run_2")
	rc2.ContainerID = "c2"
	reg.Register(rc2)

	checker := &mockContainerChecker{
		alive: map[string]bool{"c1": true, "c2": true},
	}

	lc := NewLivenessChecker(reg, checker)
	lc.CheckOnce(context.Background())

	if reg.Count() != 2 {
		t.Errorf("expected 2 runs (all alive), got %d", reg.Count())
	}
}
