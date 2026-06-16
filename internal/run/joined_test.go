package run

import (
	"os"
	"testing"
)

func TestAttachedAgents_RegisterCountRelease(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	const runID = "run_test_join"

	if got := attachedCount(runID); got != 0 {
		t.Fatalf("initial count = %d, want 0", got)
	}

	idx1, release1, err := registerJoinedAgent(runID)
	if err != nil {
		t.Fatalf("register 1: %v", err)
	}
	if idx1 != 1 {
		t.Fatalf("first index = %d, want 1", idx1)
	}
	if got := attachedCount(runID); got != 1 {
		t.Fatalf("count after 1 register = %d, want 1", got)
	}

	idx2, release2, err := registerJoinedAgent(runID)
	if err != nil {
		t.Fatalf("register 2: %v", err)
	}
	if idx2 != 2 {
		t.Fatalf("second index = %d, want 2", idx2)
	}
	if got := attachedCount(runID); got != 2 {
		t.Fatalf("count after 2 registers = %d, want 2", got)
	}

	release1()
	if got := attachedCount(runID); got != 1 {
		t.Fatalf("count after 1 release = %d, want 1", got)
	}
	release2()
	if got := attachedCount(runID); got != 0 {
		t.Fatalf("count after 2 releases = %d, want 0", got)
	}
}

func TestAttachedAgents_PrunesDeadPid(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	const runID = "run_test_join_dead"

	dir := attachedAgentsDir(runID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A pid that is essentially never alive.
	if err := os.WriteFile(dir+"/999999", []byte("999999"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := attachedCount(runID); got != 0 {
		t.Fatalf("count with only a dead pid = %d, want 0 (pruned)", got)
	}
	if _, err := os.Stat(dir + "/999999"); !os.IsNotExist(err) {
		t.Fatalf("dead pid entry should have been pruned")
	}
}
