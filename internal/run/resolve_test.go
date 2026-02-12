package run

import (
	"testing"
	"time"
)

// newTestManager creates a Manager with pre-populated runs for testing.
// No container runtime is needed since Resolve only reads the in-memory map.
func newTestManager(runs map[string]*Run) *Manager {
	return &Manager{
		runs: runs,
	}
}

func TestResolve_ExactID(t *testing.T) {
	r := &Run{ID: "run_aabbccddeeff", Name: "my-agent", CreatedAt: time.Now()}
	m := newTestManager(map[string]*Run{r.ID: r})

	matches, err := m.Resolve("run_aabbccddeeff")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].ID != "run_aabbccddeeff" {
		t.Errorf("expected ID run_aabbccddeeff, got %s", matches[0].ID)
	}
}

func TestResolve_ExactID_NotFound(t *testing.T) {
	m := newTestManager(map[string]*Run{})

	_, err := m.Resolve("run_aabbccddeeff")
	if err == nil {
		t.Fatal("expected error for non-existent ID")
	}
}

func TestResolve_IDPrefix(t *testing.T) {
	r := &Run{ID: "run_aabbccddeeff", Name: "my-agent", CreatedAt: time.Now()}
	m := newTestManager(map[string]*Run{r.ID: r})

	matches, err := m.Resolve("run_aabb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].ID != "run_aabbccddeeff" {
		t.Errorf("expected ID run_aabbccddeeff, got %s", matches[0].ID)
	}
}

func TestResolve_IDPrefix_MultipleMatches(t *testing.T) {
	r1 := &Run{ID: "run_aabb11223344", Name: "agent-1", CreatedAt: time.Now()}
	r2 := &Run{ID: "run_aabb55667788", Name: "agent-2", CreatedAt: time.Now().Add(-time.Hour)}
	m := newTestManager(map[string]*Run{r1.ID: r1, r2.ID: r2})

	matches, err := m.Resolve("run_aabb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	// Should be sorted newest first
	if matches[0].CreatedAt.Before(matches[1].CreatedAt) {
		t.Error("expected matches sorted newest first")
	}
}

func TestResolve_ExactName(t *testing.T) {
	r := &Run{ID: "run_aabbccddeeff", Name: "my-agent", CreatedAt: time.Now()}
	m := newTestManager(map[string]*Run{r.ID: r})

	matches, err := m.Resolve("my-agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Name != "my-agent" {
		t.Errorf("expected name my-agent, got %s", matches[0].Name)
	}
}

func TestResolve_NameMultipleMatches(t *testing.T) {
	r1 := &Run{ID: "run_111111111111", Name: "my-agent", CreatedAt: time.Now()}
	r2 := &Run{ID: "run_222222222222", Name: "my-agent", CreatedAt: time.Now().Add(-time.Hour)}
	r3 := &Run{ID: "run_333333333333", Name: "other-agent", CreatedAt: time.Now()}
	m := newTestManager(map[string]*Run{r1.ID: r1, r2.ID: r2, r3.ID: r3})

	matches, err := m.Resolve("my-agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	// Should be sorted newest first
	if matches[0].ID != "run_111111111111" {
		t.Errorf("expected newest run first, got %s", matches[0].ID)
	}
}

func TestResolve_NoMatch(t *testing.T) {
	r := &Run{ID: "run_aabbccddeeff", Name: "my-agent", CreatedAt: time.Now()}
	m := newTestManager(map[string]*Run{r.ID: r})

	_, err := m.Resolve("nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent name")
	}
}

func TestResolve_IDPrefixFallsToName(t *testing.T) {
	// A run whose name starts with "run_" (unlikely but should work)
	r := &Run{ID: "run_aabbccddeeff", Name: "run_my_custom", CreatedAt: time.Now()}
	m := newTestManager(map[string]*Run{r.ID: r})

	// "run_my" doesn't prefix-match any ID, so should fall through to name match
	// But "run_my_custom" isn't a valid ID, so name match won't trigger either
	// This should match nothing via prefix, then try name "run_my" which also matches nothing
	_, err := m.Resolve("run_my")
	if err == nil {
		t.Fatal("expected error for no match")
	}
}
