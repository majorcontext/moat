package run

import (
	"strings"
	"testing"
)

func TestGenerateID(t *testing.T) {
	id1 := generateID()
	id2 := generateID()

	// IDs should have the correct prefix
	if !strings.HasPrefix(id1, "run-") {
		t.Errorf("expected ID to start with 'run-', got %s", id1)
	}

	// IDs should be unique
	if id1 == id2 {
		t.Errorf("expected unique IDs, got %s and %s", id1, id2)
	}

	// IDs should have expected length (run- + 12 hex chars)
	if len(id1) != 16 {
		t.Errorf("expected ID length 16, got %d (%s)", len(id1), id1)
	}
}

func TestRunStates(t *testing.T) {
	// Verify state constants are defined
	states := []State{
		StateCreated,
		StateStarting,
		StateRunning,
		StateStopping,
		StateStopped,
		StateFailed,
	}

	for _, s := range states {
		if s == "" {
			t.Error("state should not be empty")
		}
	}
}

func TestOptions(t *testing.T) {
	opts := Options{
		Name:      "test-agent",
		Workspace: "/tmp/test",
		Grants:    []string{"github", "aws:s3.read"},
	}

	if opts.Name != "test-agent" {
		t.Errorf("expected name 'test-agent', got %s", opts.Name)
	}
	if opts.Workspace != "/tmp/test" {
		t.Errorf("expected workspace '/tmp/test', got %s", opts.Workspace)
	}
	if len(opts.Grants) != 2 {
		t.Errorf("expected 2 grants, got %d", len(opts.Grants))
	}
}
