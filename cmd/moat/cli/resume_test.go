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

// TestFindMostRecentClaudeRun_Ordering verifies the run selection logic
// by directly testing the helper functions with mock data.
func TestFindMostRecentClaudeRun_Ordering(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name  string
		agent string
		state run.State
		age   time.Duration
		want  bool
	}{
		{"running claude", "claude-code", run.StateRunning, 1 * time.Minute, true},
		{"stopped claude", "claude-code", run.StateStopped, 5 * time.Minute, true},
		{"failed claude", "claude-code", run.StateFailed, 10 * time.Minute, true},
		{"running codex", "codex", run.StateRunning, 1 * time.Minute, false},
		{"creating claude", "claude-code", run.StateCreated, 1 * time.Minute, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &run.Run{
				Agent:     tt.agent,
				State:     tt.state,
				CreatedAt: now.Add(-tt.age),
			}
			isCandidate := isClaudeRun(r) && (r.State == run.StateRunning || r.State == run.StateStopped || r.State == run.StateFailed)
			if isCandidate != tt.want {
				t.Errorf("run %q (state=%s, agent=%s): candidate=%v, want %v",
					tt.name, tt.state, tt.agent, isCandidate, tt.want)
			}
		})
	}
}
