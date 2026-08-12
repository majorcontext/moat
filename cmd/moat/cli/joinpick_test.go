package cli

import (
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
