package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

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

	// manager.List() (the real caller) iterates a map and is unordered per
	// call. renderPicker numbers candidates by slice index, so without a
	// deterministic sort the same run gets a different number across
	// invocations — a remembered "2" could attach to a different run.
	older := &run.Run{ID: "run_old", Workspace: "/work", State: run.StateRunning, JoinableAgents: []string{"claude"}, CreatedAt: time.Unix(100, 0)}
	newer := &run.Run{ID: "run_new", Workspace: "/work", State: run.StateRunning, JoinableAgents: []string{"claude"}, CreatedAt: time.Unix(200, 0)}
	elsewhereOlder := &run.Run{ID: "run_old_far", Workspace: "/other", State: run.StateRunning, JoinableAgents: []string{"claude"}, CreatedAt: time.Unix(100, 0)}
	elsewhereNewer := &run.Run{ID: "run_new_far", Workspace: "/another", State: run.StateRunning, JoinableAgents: []string{"claude"}, CreatedAt: time.Unix(200, 0)}

	t.Run("sorts local candidates newest-first regardless of input order", func(t *testing.T) {
		got, widened := inferJoinCandidates([]*run.Run{older, newer}, "/work", "claude", claude)
		if widened {
			t.Fatal("expected a local match, not widened")
		}
		if len(got) != 2 || got[0].ID != "run_new" || got[1].ID != "run_old" {
			t.Errorf("expected newest-first order; got %v", got)
		}
	})

	// Companion: the widened path returns a different slice (`all` instead of
	// `local`), so the sort must cover it too, not just the local branch.
	t.Run("sorts widened candidates newest-first regardless of input order", func(t *testing.T) {
		got, widened := inferJoinCandidates([]*run.Run{elsewhereOlder, elsewhereNewer}, "/work", "claude", claude)
		if !widened {
			t.Fatal("expected widening since neither run is in /work")
		}
		if len(got) != 2 || got[0].ID != "run_new_far" || got[1].ID != "run_old_far" {
			t.Errorf("expected newest-first order; got %v", got)
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
		got, err := pickJoinRun(strings.NewReader(""), io.Discard, []*run.Run{one}, "claude", "claude", false, true, true)
		if err != nil || got != one {
			t.Errorf("expected auto-select; got %v, %v", got, err)
		}
	})

	t.Run("single WIDENED candidate still prompts", func(t *testing.T) {
		// Companion to the auto-select case. Attaching to another workspace's
		// run means using that run's credentials, so it must be confirmed.
		// Assert the picker actually ran (via its output), not just the return
		// value — the return value alone is satisfied even if the widened
		// guard is deleted and case 1 falls straight through to auto-select.
		var out bytes.Buffer
		got, err := pickJoinRun(strings.NewReader("1\n"), &out, []*run.Run{one}, "claude", "claude", true, true, true)
		if err != nil || got != one {
			t.Errorf("expected prompted selection; got %v, %v", got, err)
		}
		if !strings.Contains(out.String(), "No running runs in this workspace") {
			t.Errorf("expected the widened picker to actually render; got %q", out.String())
		}
		if !strings.Contains(out.String(), "Select [1-1]:") {
			t.Errorf("expected a selection prompt to actually be shown; got %q", out.String())
		}
	})

	t.Run("non-TTY errors with the IDs", func(t *testing.T) {
		_, err := pickJoinRun(strings.NewReader(""), io.Discard, []*run.Run{one, two}, "claude", "claude", false, false, true)
		if err == nil {
			t.Fatal("expected an error without a TTY")
		}
		for _, id := range []string{"run_only", "run_two"} {
			if !strings.Contains(err.Error(), id) {
				t.Errorf("error should list %s; got %q", id, err)
			}
		}
	})

	t.Run("zero candidates, nothing running at all", func(t *testing.T) {
		_, err := pickJoinRun(strings.NewReader(""), io.Discard, nil, "claude", "claude", false, true, false)
		if err == nil {
			t.Fatal("expected an error with no candidates")
		}
		if !strings.Contains(err.Error(), "start one") {
			t.Errorf("error should tell the user to start a run; got %q", err)
		}
	})

	// Companion: candidates are empty either because nothing is running at
	// all, or because something is running but can't host this agent. Those
	// imply different next steps (start a run vs. recreate one with this
	// agent), so the messages must differ.
	t.Run("zero candidates, runs are running but none can host the agent", func(t *testing.T) {
		_, err := pickJoinRun(strings.NewReader(""), io.Discard, nil, "claude", "claude", false, true, true)
		if err == nil {
			t.Fatal("expected an error with no candidates")
		}
		if !strings.Contains(err.Error(), "agents:") {
			t.Errorf("error should point at moat.yaml's agents: list; got %q", err)
		}
		if strings.Contains(err.Error(), "start one") {
			t.Errorf("this case must not suggest starting a run; got %q", err)
		}
	})

	// I3 regression: `moat join openai` must not suggest running `moat
	// openai` — that's not a command. The diagnosis half of each message
	// keeps agentArg (what the user typed); the remedy half (the `moat
	// <name>` / `agents: [<name>]` suggestion) must use the canonical name.
	t.Run("zero candidates, remedy names the canonical agent, not the alias", func(t *testing.T) {
		_, err := pickJoinRun(strings.NewReader(""), io.Discard, nil, "openai", "codex", false, true, false)
		if err == nil {
			t.Fatal("expected an error with no candidates")
		}
		if !strings.Contains(err.Error(), "moat codex") {
			t.Errorf("error should suggest `moat codex`; got %q", err)
		}
		if strings.Contains(err.Error(), "moat openai") {
			t.Errorf("error must not suggest the non-existent `moat openai`; got %q", err)
		}
	})

	// Companion: the "runs exist but none can host it" remedy must also use
	// the canonical name, while the diagnosis half keeps the typed alias.
	t.Run("zero candidates but running, remedy names the canonical agent, diagnosis names the alias", func(t *testing.T) {
		_, err := pickJoinRun(strings.NewReader(""), io.Discard, nil, "openai", "codex", false, true, true)
		if err == nil {
			t.Fatal("expected an error with no candidates")
		}
		if !strings.Contains(err.Error(), "host openai") {
			t.Errorf("diagnosis should name what the user typed (openai); got %q", err)
		}
		if !strings.Contains(err.Error(), "Add codex to") {
			t.Errorf("remedy should name the canonical agent (codex); got %q", err)
		}
	})
}

// TestTwoArgMultiMatchUsesPicker locks in the shape runJoin's two-arg path
// relies on: when resolveRunningRunArg finds several running runs sharing a
// name (common, since nothing enforces run-name uniqueness and moat.yaml's
// `name:` field puts every run in a project under one name), pickJoinRun
// must pick interactively rather than error — matching the shorthand form.
func TestTwoArgMultiMatchUsesPicker(t *testing.T) {
	a := &run.Run{ID: "run_a", Name: "moat-dev", State: run.StateRunning, JoinableAgents: []string{"claude"}}
	b := &run.Run{ID: "run_b", Name: "moat-dev", State: run.StateRunning, JoinableAgents: []string{"claude"}}

	// TTY: selecting 2 picks the second candidate.
	got, err := pickJoinRun(strings.NewReader("2\n"), io.Discard, []*run.Run{a, b}, "claude", "claude", false, true, true)
	if err != nil {
		t.Fatalf("pickJoinRun: %v", err)
	}
	if got.ID != "run_b" {
		t.Errorf("selected %s, want run_b", got.ID)
	}

	// Companion: no TTY still errors with both IDs listed.
	_, err = pickJoinRun(strings.NewReader(""), io.Discard, []*run.Run{a, b}, "claude", "claude", false, false, true)
	if err == nil {
		t.Fatal("non-TTY should error rather than prompt")
	}
	for _, id := range []string{"run_a", "run_b"} {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("error should list %s; got %q", id, err)
		}
	}
}
