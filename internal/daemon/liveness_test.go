package daemon

import (
	"context"
	"errors"
	"testing"
	"time"
)

type mockContainerChecker struct {
	alive  map[string]bool
	err    map[string]error // per-container error (nil = no error)
	called map[string]int   // track call counts
}

func (m *mockContainerChecker) IsContainerRunning(_ context.Context, id string) (bool, error) {
	if m.called != nil {
		m.called[id]++
	}
	if m.err != nil {
		if err, ok := m.err[id]; ok && err != nil {
			return false, err
		}
	}
	return m.alive[id], nil
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

func TestLivenessChecker_TransientErrorsRequireThreshold(t *testing.T) {
	reg := NewRegistry()
	rc := NewRunContext("run_transient")
	rc.ContainerID = "flaky_container"
	reg.Register(rc)

	checker := &mockContainerChecker{
		alive:  map[string]bool{"flaky_container": false},
		err:    map[string]error{"flaky_container": errors.New("docker inspect: connection refused")},
		called: make(map[string]int),
	}

	lc := NewLivenessChecker(reg, checker)

	// First check — error, but below threshold.
	lc.CheckOnce(context.Background())
	if reg.Count() != 1 {
		t.Fatalf("expected run to survive first transient error, got count %d", reg.Count())
	}

	// Second check — still below threshold.
	lc.CheckOnce(context.Background())
	if reg.Count() != 1 {
		t.Fatalf("expected run to survive second transient error, got count %d", reg.Count())
	}

	// Third check — reaches threshold (default 3), should be removed.
	lc.CheckOnce(context.Background())
	if reg.Count() != 0 {
		t.Fatalf("expected run to be removed after %d consecutive failures, got count %d", defaultMaxFailures, reg.Count())
	}
}

func TestLivenessChecker_ConfirmedDeadImmediate(t *testing.T) {
	reg := NewRegistry()
	rc := NewRunContext("run_dead_immediate")
	rc.ContainerID = "dead_container"
	reg.Register(rc)

	// No error, not alive — confirmed dead.
	checker := &mockContainerChecker{
		alive: map[string]bool{"dead_container": false},
	}

	lc := NewLivenessChecker(reg, checker)

	// A single check should remove it immediately (confirmed dead, no error).
	lc.CheckOnce(context.Background())
	if reg.Count() != 0 {
		t.Fatalf("expected confirmed-dead container to be removed immediately, got count %d", reg.Count())
	}
}

func TestLivenessChecker_RecoveryResetsCount(t *testing.T) {
	reg := NewRegistry()
	rc := NewRunContext("run_recovery")
	rc.ContainerID = "recovering_container"
	reg.Register(rc)

	transientErr := errors.New("docker inspect: timeout")
	checker := &mockContainerChecker{
		alive:  map[string]bool{"recovering_container": false},
		err:    map[string]error{"recovering_container": transientErr},
		called: make(map[string]int),
	}

	lc := NewLivenessChecker(reg, checker)

	// Two transient failures.
	lc.CheckOnce(context.Background())
	lc.CheckOnce(context.Background())
	if reg.Count() != 1 {
		t.Fatalf("expected run to survive 2 transient errors, got count %d", reg.Count())
	}

	// Now the container recovers (alive, no error).
	checker.alive["recovering_container"] = true
	delete(checker.err, "recovering_container")

	lc.CheckOnce(context.Background())
	if reg.Count() != 1 {
		t.Fatalf("expected run to survive after recovery, got count %d", reg.Count())
	}

	// Subsequent failures should start from zero again.
	checker.alive["recovering_container"] = false
	checker.err["recovering_container"] = transientErr

	lc.CheckOnce(context.Background())
	lc.CheckOnce(context.Background())
	if reg.Count() != 1 {
		t.Fatalf("expected run to survive 2 failures after reset, got count %d", reg.Count())
	}

	// Third failure after reset — should now be removed.
	lc.CheckOnce(context.Background())
	if reg.Count() != 0 {
		t.Fatalf("expected run to be removed after threshold reached again, got count %d", reg.Count())
	}
}

func TestLivenessChecker_ReapsStaleUnstartedRun(t *testing.T) {
	// A run that claimed its device at registration but whose CLI died before
	// container-create has no ContainerID and no container to health-check.
	// Once it is far older than any legitimate build it must be reaped so its
	// onCleanup (which revokes the serial claim) fires, rather than leaking the
	// claim until the daemon restarts.
	reg := NewRegistry()
	rc := NewRunContext("run_stuck")
	// Holds a serial claim, no ContainerID, registered well beyond the grace.
	rc.SerialDevices = []SerialDeviceSpec{{Name: "esp32", Path: "/dev/ttyUSB0"}}
	rc.RegisteredAt = time.Now().Add(-staleUnstartedGrace - time.Minute)
	token := reg.Register(rc)

	checker := &mockContainerChecker{alive: map[string]bool{}}
	var cleanedUp string
	lc := NewLivenessChecker(reg, checker)
	lc.SetOnCleanup(func(tok, _ string) { cleanedUp = tok })

	lc.CheckOnce(context.Background())

	if reg.Count() != 0 {
		t.Errorf("stale unstarted run should be reaped, still have %d", reg.Count())
	}
	if cleanedUp != token {
		t.Errorf("onCleanup must fire for the reaped run so the device claim is revoked; got %q", cleanedUp)
	}
}

func TestLivenessChecker_KeepsRecentUnstartedRun(t *testing.T) {
	// Companion: a run still within the grace is mid-Create (e.g. building its
	// image) and must NOT be reaped — doing so would drop its device claim and
	// fail the in-flight Create.
	reg := NewRegistry()
	rc := NewRunContext("run_building")
	rc.RegisteredAt = time.Now().Add(-time.Minute) // recent
	reg.Register(rc)

	checker := &mockContainerChecker{alive: map[string]bool{}}
	lc := NewLivenessChecker(reg, checker)
	lc.CheckOnce(context.Background())

	if reg.Count() != 1 {
		t.Error("a recently-registered unstarted run must not be reaped mid-Create")
	}
}

func TestLivenessChecker_KeepsStaleUnstartedNonSerialRun(t *testing.T) {
	// Companion to the reap: a run with NO serial claim that is stuck without a
	// container (a slow image build) must NOT be reaped, however old — reaping
	// it would turn a slow-but-alive Create into a silent failure, and there is
	// no device claim to recover.
	reg := NewRegistry()
	rc := NewRunContext("run_slow_build")
	rc.RegisteredAt = time.Now().Add(-staleUnstartedGrace - time.Hour) // very old
	reg.Register(rc)

	checker := &mockContainerChecker{alive: map[string]bool{}}
	lc := NewLivenessChecker(reg, checker)
	lc.CheckOnce(context.Background())

	if reg.Count() != 1 {
		t.Error("a stale non-serial unstarted run must not be reaped")
	}
}
