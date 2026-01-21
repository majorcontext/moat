package run

import (
	"strings"
	"testing"
)

func TestGenerateID(t *testing.T) {
	id1 := generateID()
	id2 := generateID()

	// IDs should have the correct prefix (run_ with underscore)
	if !strings.HasPrefix(id1, "run_") {
		t.Errorf("expected ID to start with 'run_', got %s", id1)
	}

	// IDs should be unique
	if id1 == id2 {
		t.Errorf("expected unique IDs, got %s and %s", id1, id2)
	}

	// IDs should have expected length (run_ + 12 hex chars = 16 total)
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

func TestWorkspaceToClaudeDir(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "unix absolute path",
			input:    "/home/alice/projects/myapp",
			expected: "-home-alice-projects-myapp",
		},
		{
			name:     "simple path",
			input:    "/tmp/workspace",
			expected: "-tmp-workspace",
		},
		{
			name:     "deep nested path",
			input:    "/Users/dev/Documents/code/project/subdir",
			expected: "-Users-dev-Documents-code-project-subdir",
		},
		{
			name:     "root path",
			input:    "/workspace",
			expected: "-workspace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := workspaceToClaudeDir(tt.input)
			if result != tt.expected {
				t.Errorf("workspaceToClaudeDir(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
