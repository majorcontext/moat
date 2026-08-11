package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/ui"
)

// TestAgentVerbForInit is a regression test for the moat-init special case in
// ResolveAgentField's wiring: RunProvider's ConfigureAgent hook (called before
// the agent-name block) sets cfg.Agent for "init" runs, so "init" itself is
// not a usable verb — it must be swapped for the already-resolved cfg.Agent
// rather than clobbering it.
func TestAgentVerbForInit(t *testing.T) {
	tests := []struct {
		name   string
		rcName string
		agent  string
		want   string
	}{
		{"init reuses the auto-detected agent instead of the literal name", "init", "claude", "claude"},
		// Companion: every other provider's own name is a real agent verb and
		// must pass through unchanged.
		{"non-init providers use their own name as the verb", "claude", "codex", "claude"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Agent: tt.agent}
			if got := agentVerbFor(tt.rcName, cfg); got != tt.want {
				t.Errorf("agentVerbFor(%q, cfg.Agent=%q) = %q, want %q", tt.rcName, tt.agent, got, tt.want)
			}
		})
	}
}

// TestResolveProviderAgentFieldWarnsAcrossConfigureAgentHooks is a regression
// test for the copilot/pi warning gap: their ConfigureAgent hooks
// unconditionally overwrite cfg.Agent with their own name (exactly like
// init's does), which erases the moat.yaml value before ResolveAgentField's
// conflict check ever sees it — so a conflicting `agent:` silently produced
// no warning for those two commands, unlike claude/codex/gemini whose
// ConfigureAgent hooks don't touch cfg.Agent at all. resolveProviderAgentField
// restores the pre-ConfigureAgent snapshot before resolving, so the conflict
// check is uniform across all six provider commands.
func TestResolveProviderAgentFieldWarnsAcrossConfigureAgentHooks(t *testing.T) {
	tests := []struct {
		name               string
		rcName             string
		preConfigureAgent  string // cfg.Agent before the simulated ConfigureAgent stomp (i.e. what moat.yaml said)
		postConfigureAgent string // cfg.Agent after the simulated ConfigureAgent stomp (its own provider name)
		wantAgent          string
		wantWarn           bool
	}{
		{
			name:               "copilot with a conflicting agent: warns",
			rcName:             "copilot",
			preConfigureAgent:  "claude",
			postConfigureAgent: "copilot",
			wantAgent:          "copilot",
			wantWarn:           true,
		},
		// Companion: no conflicting field, stays silent.
		{
			name:               "copilot with no conflicting agent: stays silent",
			rcName:             "copilot",
			preConfigureAgent:  "",
			postConfigureAgent: "copilot",
			wantAgent:          "copilot",
			wantWarn:           false,
		},
		{
			name:               "pi with a conflicting agent: warns",
			rcName:             "pi",
			preConfigureAgent:  "codex",
			postConfigureAgent: "pi",
			wantAgent:          "pi",
			wantWarn:           true,
		},
		// Companion: no conflicting field, stays silent.
		{
			name:               "pi with no conflicting agent: stays silent",
			rcName:             "pi",
			preConfigureAgent:  "pi",
			postConfigureAgent: "pi",
			wantAgent:          "pi",
			wantWarn:           false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			orig := ui.Writer()
			ui.SetWriter(&buf)
			t.Cleanup(func() { ui.SetWriter(orig) })

			// Simulate what RunProvider does: snapshot cfg.Agent, then run a
			// ConfigureAgent hook that unconditionally stomps it (as copilot,
			// pi, and init all do).
			cfg := &config.Config{Agent: tt.preConfigureAgent}
			preConfigureAgent := cfg.Agent
			cfg.Agent = tt.postConfigureAgent

			resolveProviderAgentField(tt.rcName, cfg, preConfigureAgent)

			if cfg.Agent != tt.wantAgent {
				t.Errorf("cfg.Agent = %q, want %q", cfg.Agent, tt.wantAgent)
			}
			if gotWarn := buf.Len() > 0; gotWarn != tt.wantWarn {
				t.Errorf("warned = %v, want %v (output: %q)", gotWarn, tt.wantWarn, buf.String())
			}
		})
	}
}

func TestBuildGrants(t *testing.T) {
	tests := []struct {
		name         string
		autoDetected string
		configGrants []string
		flagGrants   []string
		want         []string
	}{
		{
			name:         "auto-detected claude with no explicit grants",
			autoDetected: "claude",
			want:         []string{"claude"},
		},
		{
			name:         "auto-detected claude suppressed when anthropic is explicit",
			autoDetected: "claude",
			flagGrants:   []string{"anthropic"},
			want:         []string{"anthropic"},
		},
		{
			name:         "auto-detected anthropic suppressed when claude is explicit",
			autoDetected: "anthropic",
			configGrants: []string{"claude"},
			want:         []string{"claude"},
		},
		{
			name:         "auto-detected gemini NOT suppressed when anthropic is explicit",
			autoDetected: "gemini",
			flagGrants:   []string{"anthropic"},
			want:         []string{"gemini", "anthropic"},
		},
		{
			name:         "auto-detected claude NOT suppressed when github is explicit",
			autoDetected: "claude",
			flagGrants:   []string{"github"},
			want:         []string{"claude", "github"},
		},
		{
			name:         "no auto-detected with explicit grants",
			autoDetected: "",
			configGrants: []string{"claude", "github"},
			want:         []string{"claude", "github"},
		},
		{
			name:         "deduplication across config and flag grants",
			autoDetected: "claude",
			configGrants: []string{"github"},
			flagGrants:   []string{"github", "claude"},
			want:         []string{"claude", "github"},
		},
		{
			name:         "both claude and anthropic explicit",
			autoDetected: "",
			flagGrants:   []string{"claude", "anthropic"},
			want:         []string{"claude", "anthropic"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildGrants(tt.autoDetected, tt.configGrants, tt.flagGrants)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildGrants(%q, %v, %v) = %v, want %v",
					tt.autoDetected, tt.configGrants, tt.flagGrants, got, tt.want)
			}
		})
	}
}

// gitCommitMoatYAML writes a valid moat.yaml whose name field is set to `name`
// and commits it, so tests can tell which config was loaded.
func gitCommitMoatYAML(t *testing.T, dir, name, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte("agent: test\nname: "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", msg}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestRunProviderPreflightUsesResolvedConfig is a regression test for the
// worktree config-source fix: RunProvider must call Preflight with the config
// that will actually be used — including a worktree's own moat.yaml, resolved by
// ResolveWorktreeWorkspace — not the pre-worktree workspace config.
func TestRunProviderPreflightUsesResolvedConfig(t *testing.T) {
	repoDir := initTestRepo(t)
	defer os.RemoveAll(repoDir)

	// Main branch gets moat.yaml name=from-main; a worktree branch diverges to
	// name=from-worktree so we can tell which config Preflight received.
	gitCommitMoatYAML(t, repoDir, "from-main", "add main moat.yaml")
	gitRun(t, repoDir, "checkout", "-b", "wt-branch")
	gitCommitMoatYAML(t, repoDir, "from-worktree", "diverge moat.yaml on branch")
	gitRun(t, repoDir, "checkout", "-") // back to the default branch (from-main)

	wtBase, err := os.MkdirTemp("", "test-wt-base-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(wtBase)
	t.Setenv("MOAT_WORKTREE_BASE", wtBase)

	oldDir, _ := os.Getwd()
	if chErr := os.Chdir(repoDir); chErr != nil {
		t.Fatal(chErr)
	}
	defer os.Chdir(oldDir)

	oldDryRun := DryRun
	DryRun = true // return after Preflight/BuildCommand, before ExecuteRun
	defer func() { DryRun = oldDryRun }()

	oldCheck := CheckWorktreeActive
	CheckWorktreeActive = nil
	defer func() { CheckWorktreeActive = oldCheck }()

	// resolvedNameFor drives RunProvider via a real cobra command named
	// "testprov" (RunProvider guards on cmd.CalledAs() == rc.Name), and returns
	// the cfg.Name that Preflight observed.
	resolvedNameFor := func(wtFlag string) string {
		var got string
		cmd := &cobra.Command{
			Use:           "testprov",
			SilenceUsage:  true,
			SilenceErrors: true,
			RunE: func(c *cobra.Command, a []string) error {
				return RunProvider(c, a, ProviderRunConfig{
					Name:   "testprov",
					Flags:  &ExecFlags{},
					WtFlag: wtFlag,
					Preflight: func(cfg *config.Config) error {
						if cfg != nil {
							got = cfg.Name
						}
						return nil
					},
					BuildCommand: func(_, _ string) ([]string, error) { return []string{"noop"}, nil },
				})
			},
		}
		cmd.SetArgs([]string{repoDir})
		if execErr := cmd.Execute(); execErr != nil {
			t.Fatalf("Execute(wt=%q): %v", wtFlag, execErr)
		}
		return got
	}

	// Companion 1: no worktree — Preflight sees the main workspace config.
	if got := resolvedNameFor(""); got != "from-main" {
		t.Errorf("no worktree: Preflight cfg.Name = %q, want %q", got, "from-main")
	}
	// Companion 2 (the fix): with --worktree, Preflight sees the worktree
	// branch's own moat.yaml, not the pre-worktree workspace config.
	if got := resolvedNameFor("wt-branch"); got != "from-worktree" {
		t.Errorf("worktree: Preflight cfg.Name = %q, want %q (post-worktree config)", got, "from-worktree")
	}
}
