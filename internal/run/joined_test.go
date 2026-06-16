package run

import (
	"os"
	"path/filepath"
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
	// Write an entry with index filename "1" and a dead pid as content.
	if err := os.WriteFile(filepath.Join(dir, "1"), []byte("999999"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := attachedCount(runID); got != 0 {
		t.Fatalf("count with only a dead pid = %d, want 0 (pruned)", got)
	}
	if _, err := os.Stat(dir + "/1"); !os.IsNotExist(err) {
		t.Fatalf("dead pid entry should have been pruned")
	}
}

func TestAttachedAgents_UniqueIndices(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	const runID = "run_test_join_unique"

	// Register three agents: should claim indices 1, 2, 3.
	idx1, release1, err := registerJoinedAgent(runID)
	if err != nil {
		t.Fatalf("register 1: %v", err)
	}
	idx2, release2, err := registerJoinedAgent(runID)
	if err != nil {
		t.Fatalf("register 2: %v", err)
	}
	idx3, release3, err := registerJoinedAgent(runID)
	if err != nil {
		t.Fatalf("register 3: %v", err)
	}

	if idx1 != 1 || idx2 != 2 || idx3 != 3 {
		t.Fatalf("expected indices 1,2,3 got %d,%d,%d", idx1, idx2, idx3)
	}
	if got := attachedCount(runID); got != 3 {
		t.Fatalf("count with 3 live agents = %d, want 3", got)
	}

	// Release slot 2; registering again should reclaim index 2 (lowest free).
	release2()
	if got := attachedCount(runID); got != 2 {
		t.Fatalf("count after releasing index 2 = %d, want 2", got)
	}

	idx2b, release2b, err := registerJoinedAgent(runID)
	if err != nil {
		t.Fatalf("re-register after release: %v", err)
	}
	if idx2b != 2 {
		t.Fatalf("re-registration should reclaim index 2 (lowest free), got %d", idx2b)
	}
	if got := attachedCount(runID); got != 3 {
		t.Fatalf("count after re-register = %d, want 3", got)
	}

	// All live indices must be unique.
	seen := map[int]bool{idx1: true, idx3: true, idx2b: true}
	if len(seen) != 3 {
		t.Fatalf("duplicate live indices: %d,%d,%d", idx1, idx3, idx2b)
	}

	release1()
	release3()
	release2b()
}
