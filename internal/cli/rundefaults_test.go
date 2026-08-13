package cli_test

import (
	"slices"
	"testing"

	"github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"

	// Registers all credential/agent providers so ExpandAgents (called inside
	// ApplyAgentDefaults) sees a populated registry. Same pattern as
	// agents_test.go.
	_ "github.com/majorcontext/moat/internal/providers"
)

// TestApplyAgentDefaultsDefaultsGrantsAndCommand covers the no-override path:
// no --grant flags and no explicit command, so both are populated from
// config — including a derived grant from agents:.
func TestApplyAgentDefaultsDefaultsGrantsAndCommand(t *testing.T) {
	cfg := &config.Config{
		Agents:  []string{"codex"},
		Grants:  []string{"github"},
		Command: []string{"npm", "test"},
	}
	var flagsGrants, command []string

	if err := cli.ApplyAgentDefaults(cfg, &flagsGrants, &command); err != nil {
		t.Fatalf("ApplyAgentDefaults: %v", err)
	}

	// codex's derived grant is openai; it must join the explicit "github"
	// grant in the defaulted flags list.
	for _, want := range []string{"github", "openai"} {
		if !slices.Contains(flagsGrants, want) {
			t.Errorf("expected flagsGrants to contain %q; got %v", want, flagsGrants)
		}
	}
	// AppendDerivedGrants must have written the derived grant back into
	// cfg.Grants too, since it has its own downstream readers
	// (ShouldSyncCodexLogs etc.) that never see flagsGrants.
	if !slices.Contains(cfg.Grants, "openai") {
		t.Errorf("expected cfg.Grants to contain derived grant openai; got %v", cfg.Grants)
	}
	if got := []string{"npm", "test"}; !slices.Equal(command, got) {
		t.Errorf("expected command defaulted to %v; got %v", got, command)
	}
}

// TestApplyAgentDefaultsCompanionExplicitOverridesLeftAlone is the mirror of
// the defaulting test above: when the caller already passed --grant flags or
// an explicit command, neither is overwritten by config — this is override
// semantics, not merge semantics. The derived grant must still land in
// cfg.Grants regardless, since AppendDerivedGrants always runs.
func TestApplyAgentDefaultsCompanionExplicitOverridesLeftAlone(t *testing.T) {
	cfg := &config.Config{
		Agents:  []string{"codex"},
		Grants:  []string{"github"},
		Command: []string{"npm", "test"},
	}
	flagsGrants := []string{"aws:s3.read"}
	command := []string{"bash"}

	if err := cli.ApplyAgentDefaults(cfg, &flagsGrants, &command); err != nil {
		t.Fatalf("ApplyAgentDefaults: %v", err)
	}

	if got := []string{"aws:s3.read"}; !slices.Equal(flagsGrants, got) {
		t.Errorf("explicit --grant flags must not be overwritten by config; got %v, want %v", flagsGrants, got)
	}
	if got := []string{"bash"}; !slices.Equal(command, got) {
		t.Errorf("explicit command must not be overwritten by config; got %v, want %v", command, got)
	}
	if !slices.Contains(cfg.Grants, "openai") {
		t.Errorf("derived grant must still be written back into cfg.Grants even when flags override; got %v", cfg.Grants)
	}
}

// TestApplyAgentDefaultsNilConfig ensures a nil *config.Config (moat run with
// no moat.yaml present) is handled without panicking and leaves the caller's
// slices untouched.
func TestApplyAgentDefaultsNilConfig(t *testing.T) {
	var flagsGrants, command []string
	if err := cli.ApplyAgentDefaults(nil, &flagsGrants, &command); err != nil {
		t.Fatalf("ApplyAgentDefaults(nil, ...): %v", err)
	}
	if len(flagsGrants) != 0 {
		t.Errorf("expected no grants defaulted from a nil config; got %v", flagsGrants)
	}
	if len(command) != 0 {
		t.Errorf("expected no command defaulted from a nil config; got %v", command)
	}
}

// TestApplyAgentDefaultsPropagatesExpandAgentsError checks that an invalid
// agents: entry surfaces as an error rather than being silently swallowed by
// the extraction.
func TestApplyAgentDefaultsPropagatesExpandAgentsError(t *testing.T) {
	cfg := &config.Config{Agents: []string{"not-a-real-agent"}}
	var flagsGrants, command []string
	if err := cli.ApplyAgentDefaults(cfg, &flagsGrants, &command); err == nil {
		t.Fatal("expected an error for an unknown agents: entry, got nil")
	}
}
