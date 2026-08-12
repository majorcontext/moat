package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/run"
)

func TestRunHostsAgent(t *testing.T) {
	claude := fakeJoinable{names: []string{"claude", "claude-code"}}

	tests := []struct {
		name string
		r    *run.Run
		want bool
	}{
		{"member of the set", &run.Run{JoinableAgents: []string{"claude"}}, true},
		{"non-member", &run.Run{JoinableAgents: []string{"codex"}}, false},
		{"empty set hosts nothing", &run.Run{JoinableAgents: []string{}}, false},
		{"nil set falls back to the agent string", &run.Run{Agent: "claude", JoinableAgents: nil}, true},
		{"nil set with a stale agent string", &run.Run{Agent: "vibrant-code", JoinableAgents: nil}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runHostsAgent(tt.r, "claude", claude); got != tt.want {
				t.Errorf("runHostsAgent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInferJoinCandidates(t *testing.T) {
	claude := fakeJoinable{names: []string{"claude"}}
	here := &run.Run{ID: "run_here", Workspace: "/work", State: run.StateRunning, JoinableAgents: []string{"claude"}}
	elsewhere := &run.Run{ID: "run_far", Workspace: "/other", State: run.StateRunning, JoinableAgents: []string{"claude"}}
	stopped := &run.Run{ID: "run_old", Workspace: "/work", State: run.StateStopped, JoinableAgents: []string{"claude"}}
	codexOnly := &run.Run{ID: "run_cx", Workspace: "/work", State: run.StateRunning, JoinableAgents: []string{"codex"}}

	t.Run("prefers the current workspace", func(t *testing.T) {
		got, widened := inferJoinCandidates([]*run.Run{here, elsewhere}, "/work", "claude", claude)
		if len(got) != 1 || got[0].ID != "run_here" {
			t.Errorf("expected only the cwd run; got %v", got)
		}
		if widened {
			t.Error("should not widen when the workspace has a match")
		}
	})

	t.Run("widens when the workspace has none", func(t *testing.T) {
		got, widened := inferJoinCandidates([]*run.Run{elsewhere}, "/work", "claude", claude)
		if len(got) != 1 || got[0].ID != "run_far" {
			t.Errorf("expected the widened match; got %v", got)
		}
		if !widened {
			t.Error("should report widening so the caller can disclose it")
		}
	})

	t.Run("excludes stopped runs", func(t *testing.T) {
		got, _ := inferJoinCandidates([]*run.Run{stopped}, "/work", "claude", claude)
		if len(got) != 0 {
			t.Errorf("stopped runs must never be candidates; got %v", got)
		}
	})

	t.Run("capability filter narrows to the viable run", func(t *testing.T) {
		got, _ := inferJoinCandidates([]*run.Run{codexOnly, here}, "/work", "claude", claude)
		if len(got) != 1 || got[0].ID != "run_here" {
			t.Errorf("expected only the claude-capable run; got %v", got)
		}
	})
}

func TestReadSelection(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		n       int
		want    int
		wantErr bool
	}{
		{"valid choice", "2\n", 3, 2, false},
		{"first choice", "1\n", 2, 1, false},
		{"out of range high", "5\n", 2, 0, true},
		{"out of range low", "0\n", 2, 0, true},
		{"non-numeric", "abc\n", 2, 0, true},
		{"EOF", "", 2, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readSelection(strings.NewReader(tt.input), tt.n)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				// Invalid input aborts; it never retries.
				if !strings.Contains(err.Error(), "moat join <run> <agent>") {
					t.Errorf("error should point at the explicit form; got %q", err)
				}
				return
			}
			if got != tt.want {
				t.Errorf("readSelection() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRenderPickerHeaders(t *testing.T) {
	candidates := []*run.Run{
		{ID: "run_a1b2c3d4e5f6", Name: "happy-otter", Workspace: "/work", JoinableAgents: []string{"claude"}},
	}

	var normal bytes.Buffer
	renderPicker(&normal, candidates, "claude", false)
	if !strings.Contains(normal.String(), "Multiple running runs can host claude") {
		t.Errorf("unexpected header: %q", normal.String())
	}
	if strings.Contains(normal.String(), "WORKSPACE") {
		t.Error("non-widened picker should not show the WORKSPACE column")
	}

	// Companion: the widened header says so and shows workspaces, because the
	// user is about to attach outside their current directory.
	var widened bytes.Buffer
	renderPicker(&widened, candidates, "claude", true)
	if !strings.Contains(widened.String(), "No running runs in this workspace") {
		t.Errorf("widened header missing: %q", widened.String())
	}
	if !strings.Contains(widened.String(), "WORKSPACE") {
		t.Error("widened picker should show the WORKSPACE column")
	}
}

func TestPickJoinRun(t *testing.T) {
	one := &run.Run{ID: "run_only", Name: "solo", JoinableAgents: []string{"claude"}}
	two := &run.Run{ID: "run_two", Name: "other", JoinableAgents: []string{"claude"}}

	t.Run("single candidate auto-selects", func(t *testing.T) {
		got, err := pickJoinRun(strings.NewReader(""), io.Discard, []*run.Run{one}, "claude", false, true)
		if err != nil || got != one {
			t.Errorf("expected auto-select; got %v, %v", got, err)
		}
	})

	t.Run("single WIDENED candidate still prompts", func(t *testing.T) {
		// Companion to the auto-select case. Attaching to another workspace's
		// run means using that run's credentials, so it must be confirmed.
		got, err := pickJoinRun(strings.NewReader("1\n"), io.Discard, []*run.Run{one}, "claude", true, true)
		if err != nil || got != one {
			t.Errorf("expected prompted selection; got %v, %v", got, err)
		}
	})

	t.Run("non-TTY errors with the IDs", func(t *testing.T) {
		_, err := pickJoinRun(strings.NewReader(""), io.Discard, []*run.Run{one, two}, "claude", false, false)
		if err == nil {
			t.Fatal("expected an error without a TTY")
		}
		for _, id := range []string{"run_only", "run_two"} {
			if !strings.Contains(err.Error(), id) {
				t.Errorf("error should list %s; got %q", id, err)
			}
		}
	})

	t.Run("zero candidates errors", func(t *testing.T) {
		if _, err := pickJoinRun(strings.NewReader(""), io.Discard, nil, "claude", false, true); err == nil {
			t.Error("expected an error with no candidates")
		}
	})
}
