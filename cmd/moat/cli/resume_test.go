package cli

import (
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/run"
)

func TestResumeCommand_FlagRegistration(t *testing.T) {
	// resumeCmd is registered in init(), verify it is a subcommand of root
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "resume [run]" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("resume command not registered on root")
	}
}

func TestResumeCommand_NoYoloFlag(t *testing.T) {
	f := resumeCmd.Flags().Lookup("noyolo")
	if f == nil {
		t.Fatal("--noyolo flag not registered on resume command")
	}
	if f.DefValue != "false" {
		t.Errorf("--noyolo default = %q, want %q", f.DefValue, "false")
	}
}

func TestResumeCommand_HasExecFlags(t *testing.T) {
	// Verify common exec flags are present (added by AddExecFlags)
	for _, name := range []string{"grant", "name", "detach", "rebuild"} {
		f := resumeCmd.Flags().Lookup(name)
		if f == nil {
			t.Errorf("exec flag --%s not registered on resume command", name)
		}
	}
}

func TestResumeCommand_AcceptsMaxOneArg(t *testing.T) {
	if resumeCmd.Args == nil {
		t.Fatal("resume command Args validator is nil")
	}

	// Valid: 0 args
	if err := resumeCmd.Args(resumeCmd, nil); err != nil {
		t.Errorf("resume command should accept 0 args: %v", err)
	}
	if err := resumeCmd.Args(resumeCmd, []string{"my-run"}); err != nil {
		t.Errorf("resume command should accept 1 arg: %v", err)
	}

	// Invalid: 2 args
	if err := resumeCmd.Args(resumeCmd, []string{"a", "b"}); err == nil {
		t.Error("resume command should reject 2 args")
	}
}

func TestResumeCommand_ShortDescription(t *testing.T) {
	if resumeCmd.Short == "" {
		t.Error("resume command Short description is empty")
	}
}

func TestResumeCommand_LongDescription(t *testing.T) {
	if resumeCmd.Long == "" {
		t.Error("resume command Long description is empty")
	}
}

func TestIsClaudeRun(t *testing.T) {
	tests := []struct {
		name  string
		agent string
		want  bool
	}{
		{"claude-code agent", "claude-code", true},
		{"claude agent", "claude", true},
		{"codex agent", "codex", false},
		{"gemini agent", "gemini", false},
		{"empty agent", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &run.Run{Agent: tt.agent}
			if got := isClaudeRun(r); got != tt.want {
				t.Errorf("isClaudeRun(%q) = %v, want %v", tt.agent, got, tt.want)
			}
		})
	}
}

func TestResumeCommand_Structure(t *testing.T) {
	// Verify the command uses a run argument, not workspace
	if resumeCmd.Use != "resume [run]" {
		t.Errorf("resume command Use = %q, want %q", resumeCmd.Use, "resume [run]")
	}
}

func TestResumeCommand_ClaudeFlags(t *testing.T) {
	// Verify that moat claude has --continue and --resume flags
	// (These are registered in the claude provider's RegisterCLI)

	// The resume command itself doesn't have these flags since it
	// always uses --continue. Verify the noyolo flag exists.
	noyolo := resumeCmd.Flags().Lookup("noyolo")
	if noyolo == nil {
		t.Fatal("--noyolo flag not found on resume command")
	}
}

func TestSelectResumableRun(t *testing.T) {
	now := time.Now()

	t.Run("prefers running over stopped", func(t *testing.T) {
		stopped := &run.Run{Agent: "claude-code", State: run.StateStopped, CreatedAt: now.Add(-1 * time.Minute)}
		running := &run.Run{Agent: "claude-code", State: run.StateRunning, CreatedAt: now.Add(-5 * time.Minute)}
		got := selectResumableRun([]*run.Run{stopped, running})
		if got != running {
			t.Errorf("expected running run to be preferred over stopped")
		}
	})

	t.Run("prefers newest running", func(t *testing.T) {
		older := &run.Run{Agent: "claude-code", State: run.StateRunning, CreatedAt: now.Add(-10 * time.Minute)}
		newer := &run.Run{Agent: "claude-code", State: run.StateRunning, CreatedAt: now.Add(-1 * time.Minute)}
		got := selectResumableRun([]*run.Run{older, newer})
		if got != newer {
			t.Errorf("expected newest running run")
		}
	})

	t.Run("prefers newest stopped when no running", func(t *testing.T) {
		older := &run.Run{Agent: "claude-code", State: run.StateStopped, CreatedAt: now.Add(-10 * time.Minute)}
		newer := &run.Run{Agent: "claude-code", State: run.StateFailed, CreatedAt: now.Add(-1 * time.Minute)}
		got := selectResumableRun([]*run.Run{older, newer})
		if got != newer {
			t.Errorf("expected newest stopped/failed run")
		}
	})

	t.Run("skips non-claude runs", func(t *testing.T) {
		codex := &run.Run{Agent: "codex", State: run.StateRunning, CreatedAt: now}
		claude := &run.Run{Agent: "claude-code", State: run.StateStopped, CreatedAt: now.Add(-5 * time.Minute)}
		got := selectResumableRun([]*run.Run{codex, claude})
		if got != claude {
			t.Errorf("expected codex to be skipped, got %v", got)
		}
	})

	t.Run("skips creating state", func(t *testing.T) {
		creating := &run.Run{Agent: "claude-code", State: run.StateCreated, CreatedAt: now}
		got := selectResumableRun([]*run.Run{creating})
		if got != nil {
			t.Errorf("expected nil for creating-only runs, got %v", got)
		}
	})

	t.Run("empty list", func(t *testing.T) {
		got := selectResumableRun(nil)
		if got != nil {
			t.Errorf("expected nil for empty list, got %v", got)
		}
	})
}
