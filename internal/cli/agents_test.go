// Package cli_test is an external test package (not `package cli`) because
// every agent provider package (claude, codex, gemini, copilot, pi) imports
// internal/cli itself, to register its CLI command via a cli.go file in that
// package. An internal test file (package cli) blank-importing
// internal/providers to populate the registry would therefore create a real
// import cycle: cli -> providers -> providers/claude -> cli. As an external
// test package, cli_test is a distinct package from cli, so it can import
// internal/providers without cycling back. It only needs the exported
// surface (CanonicalAgent, KnownAgentNames, ValidateAgent), so nothing here
// requires internal access.
package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/ui"

	// Registers all credential/agent providers (claude, codex, github, ...) via
	// import side effects, matching the pattern in
	// internal/provider/interfaces_test.go. Without this, the registry is empty
	// under test and CanonicalAgent/KnownAgentNames/ValidateAgent see no agents.
	_ "github.com/majorcontext/moat/internal/providers"
)

func TestCanonicalAgent(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"registered agent", "claude", "claude"},
		{"documented variant", "claude-code", "claude"},
		{"registry alias", "openai", "codex"},
		{"hallucinated value", "vibrant-code", ""},
		{"non-agent provider", "github", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cli.CanonicalAgent(tt.input); got != tt.want {
				t.Errorf("CanonicalAgent(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestKnownAgentNamesIncludesVariants(t *testing.T) {
	names := cli.KnownAgentNames()
	joined := strings.Join(names, ",")
	// "openai" is a registry alias (RegisterAlias("openai", "codex") in
	// internal/providers/codex/provider.go), not a documented variant like
	// claude-code — it must still appear here so a typo'd `agent:` warning
	// tells the user it's an accepted value.
	for _, want := range []string{"claude", "claude-code", "codex", "openai"} {
		if !strings.Contains(joined, want) {
			t.Errorf("KnownAgentNames() = %v, missing %q", names, want)
		}
	}
	// Companion: non-agent providers must not leak into the valid set.
	if strings.Contains(joined, "github") {
		t.Errorf("KnownAgentNames() = %v, should not include non-agent providers", names)
	}
}

func TestValidateAgent(t *testing.T) {
	tests := []struct {
		name      string
		agent     string
		wantAgent string
		wantWarn  bool
	}{
		{"unknown is cleared and warned", "vibrant-code", "", true},
		{"valid is preserved", "claude", "claude", false},
		{"documented variant is preserved", "claude-code", "claude-code", false},
		{"non-agent provider is cleared", "github", "", true},
		{"empty is untouched and silent", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			orig := ui.Writer()
			ui.SetWriter(&buf)
			t.Cleanup(func() { ui.SetWriter(orig) })

			cfg := &config.Config{Agent: tt.agent}
			cli.ValidateAgent(cfg)

			if cfg.Agent != tt.wantAgent {
				t.Errorf("cfg.Agent = %q, want %q", cfg.Agent, tt.wantAgent)
			}
			gotWarn := buf.Len() > 0
			if gotWarn != tt.wantWarn {
				t.Errorf("warned = %v, want %v (output: %q)", gotWarn, tt.wantWarn, buf.String())
			}
			if tt.wantWarn && !strings.Contains(buf.String(), tt.agent) {
				t.Errorf("warning should name the offending value %q; got %q", tt.agent, buf.String())
			}
		})
	}
}

func TestResolveAgentField(t *testing.T) {
	tests := []struct {
		name      string
		agent     string
		verb      string
		wantAgent string
		wantWarn  bool
	}{
		{"verb backfills an empty field", "", "claude", "claude", false},
		{"verb overrides a conflicting value", "codex", "claude", "claude", true},
		{"verb agrees with the field", "claude", "claude", "claude", false},
		{"verb agrees via variant", "claude-code", "claude", "claude", false},
		{"moat run keeps a valid field", "codex", "", "codex", false},
		{"moat run clears an invalid field", "vibrant-code", "", "", true},
	}
	// The point of this fix is that the five HasPrefix call sites downstream
	// (isAIAgent, agentImpliedDependencies, language_servers, copilot init, pi
	// staging) receive a usable value. TestAgentFieldReachesDegradationSites
	// below asserts that directly.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			orig := ui.Writer()
			ui.SetWriter(&buf)
			t.Cleanup(func() { ui.SetWriter(orig) })

			cfg := &config.Config{Agent: tt.agent}
			cli.ResolveAgentField(cfg, tt.verb)

			if cfg.Agent != tt.wantAgent {
				t.Errorf("cfg.Agent = %q, want %q", cfg.Agent, tt.wantAgent)
			}
			if gotWarn := buf.Len() > 0; gotWarn != tt.wantWarn {
				t.Errorf("warned = %v, want %v (output: %q)", gotWarn, tt.wantWarn, buf.String())
			}
		})
	}
}

func TestResolveAgentFieldWithAgentsList(t *testing.T) {
	tests := []struct {
		name      string
		agent     string
		agents    []string
		verb      string
		wantAgent string
		wantWarn  bool
	}{
		{"moat run falls back to agents[0]", "", []string{"claude", "codex"}, "", "claude", false},
		{"list order decides the fallback", "", []string{"codex", "claude"}, "", "codex", false},
		{"agent: still wins over agents[0]", "codex", []string{"claude", "codex"}, "", "codex", false},
		{"verb still wins over both", "codex", []string{"claude", "codex"}, "claude", "claude", true},
		{"agent: outside agents: warns", "gemini", []string{"claude", "codex"}, "", "gemini", true},
		// Companion: with a verb present, the "not in agents:" warning must not
		// fire — see TestResolveAgentFieldOutsideAgentsListVerbWarnsOnce for the
		// assertion that only the conflict warning appears (not both).
		{"agent: outside agents: with a verb only warns about the conflict", "gemini", []string{"claude", "codex"}, "claude", "claude", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			orig := ui.Writer()
			ui.SetWriter(&buf)
			t.Cleanup(func() { ui.SetWriter(orig) })

			cfg := &config.Config{Agent: tt.agent, Agents: tt.agents}
			cli.ResolveAgentField(cfg, tt.verb)

			if cfg.Agent != tt.wantAgent {
				t.Errorf("cfg.Agent = %q, want %q", cfg.Agent, tt.wantAgent)
			}
			if gotWarn := buf.Len() > 0; gotWarn != tt.wantWarn {
				t.Errorf("warned = %v, want %v (output: %q)", gotWarn, tt.wantWarn, buf.String())
			}
		})
	}
}

// TestResolveAgentFieldOutsideAgentsListVerbWarnsOnce is a regression test:
// the "not in agents:" warning claims the field "will run as the primary",
// which is only true on the no-verb (`moat run`) path. When a verb is
// present, ResolveAgentField immediately overwrites cfg.Agent with verb a
// few lines later, and RunProvider provisions the verb's own dependencies,
// grants, and network hosts independently of `agents:` — so the field is
// genuinely provisioned without being in the list, and the "not in agents:"
// warning must stay silent. Only the verb-conflict warning should fire.
func TestResolveAgentFieldOutsideAgentsListVerbWarnsOnce(t *testing.T) {
	var buf bytes.Buffer
	orig := ui.Writer()
	ui.SetWriter(&buf)
	t.Cleanup(func() { ui.SetWriter(orig) })

	cfg := &config.Config{Agent: "gemini", Agents: []string{"claude", "codex"}}
	cli.ResolveAgentField(cfg, "claude")

	if cfg.Agent != "claude" {
		t.Fatalf("cfg.Agent = %q, want %q", cfg.Agent, "claude")
	}
	out := buf.String()
	if got := strings.Count(out, "Warning:"); got != 1 {
		t.Errorf("expected exactly 1 warning, got %d: %q", got, out)
	}
	if strings.Contains(out, "is not in `agents:") {
		t.Errorf("the not-in-agents warning must not fire when a verb is present: %q", out)
	}
	if !strings.Contains(out, "conflicts with") {
		t.Errorf("expected the verb-conflict warning: %q", out)
	}
}

func TestExpandAgents(t *testing.T) {
	cfg := &config.Config{Agents: []string{"claude", "codex"}}
	grants, err := cli.ExpandAgents(cfg)
	if err != nil {
		t.Fatalf("ExpandAgents: %v", err)
	}

	for _, dep := range []string{"claude-code", "codex-cli"} {
		if !cli.HasDependency(cfg.Dependencies, dep) {
			t.Errorf("expected dependency %q; got %v", dep, cfg.Dependencies)
		}
	}
	// codex's grant is openai, not codex.
	if !slices.Contains(grants, "openai") {
		t.Errorf("expected grant openai; got %v", grants)
	}
	if slices.Contains(grants, "codex") {
		t.Errorf("codex must expand to the openai grant, not codex; got %v", grants)
	}
	// ExpandAgents must not mutate cfg.Grants directly — see its doc comment:
	// derived grants are returned so callers can give them lower precedence
	// than an auto-detected credential.
	if len(cfg.Grants) != 0 {
		t.Errorf("cfg.Grants should be untouched by ExpandAgents; got %v", cfg.Grants)
	}

	hosts := make([]string, 0, len(cfg.Network.Rules))
	for _, r := range cfg.Network.Rules {
		hosts = append(hosts, r.Host)
	}
	for _, want := range []string{"claude.ai", "api.openai.com"} {
		if !slices.Contains(hosts, want) {
			t.Errorf("expected host %q; got %v", want, hosts)
		}
	}
}

func TestExpandAgentsDoesNotDuplicate(t *testing.T) {
	// Companion to the expansion test: already-declared values are not repeated.
	cfg := &config.Config{
		Agents:       []string{"claude"},
		Dependencies: []string{"claude-code"},
		Grants:       []string{"claude"},
	}
	grants, err := cli.ExpandAgents(cfg)
	if err != nil {
		t.Fatalf("ExpandAgents: %v", err)
	}
	if got := countOccurrences(cfg.Dependencies, "claude-code"); got != 1 {
		t.Errorf("claude-code appears %d times, want 1: %v", got, cfg.Dependencies)
	}
	// claude is already in cfg.Grants, so it must not also come back as a
	// derived grant — the caller would otherwise re-add it.
	if len(grants) != 0 {
		t.Errorf("expected no derived grants (claude already in cfg.Grants); got %v", grants)
	}
}

// TestAppendDerivedGrants is a regression test: ExpandAgents deliberately
// returns derived grants instead of writing them into cfg.Grants (see its
// doc comment), but two downstream readers — Config.ShouldSyncCodexLogs /
// ShouldSyncGeminiLogs and buildLocalMCPConfig's grant validation in
// internal/run — read cfg.Grants directly and never see the returned slice.
// Callers must write the derived grants back with AppendDerivedGrants after
// grant precedence resolution runs.
func TestAppendDerivedGrants(t *testing.T) {
	cfg := &config.Config{Grants: []string{"github"}}
	cli.AppendDerivedGrants(cfg, []string{"openai", "github"})
	if !slices.Contains(cfg.Grants, "openai") {
		t.Errorf("expected openai appended; got %v", cfg.Grants)
	}
	if countOccurrences(cfg.Grants, "github") != 1 {
		t.Errorf("github was already present; must not be duplicated: got %v", cfg.Grants)
	}
	// A nil cfg must not panic — provider.go can still hold a nil *Config at
	// the point buildGrants runs, before it's defaulted to &config.Config{}.
	cli.AppendDerivedGrants(nil, []string{"openai"})
}

// TestExpandAgentsWriteBackEnablesCodexLogSync reproduces the regression this
// fix addresses: `agents: [codex]` with no explicit top-level grants used to
// leave cfg.Grants empty after ExpandAgents, so ShouldSyncCodexLogs (which
// reads cfg.Grants directly, not ExpandAgents' return value) silently stayed
// false and the codex session-transcript mount was never added. Writing the
// derived grants back with AppendDerivedGrants — the fix — makes cfg.Grants
// carry "openai" so ShouldSyncCodexLogs sees it.
func TestExpandAgentsWriteBackEnablesCodexLogSync(t *testing.T) {
	cfg := &config.Config{Agents: []string{"codex"}}
	derived, err := cli.ExpandAgents(cfg)
	if err != nil {
		t.Fatalf("ExpandAgents: %v", err)
	}
	cli.AppendDerivedGrants(cfg, derived)
	if !slices.Contains(cfg.Grants, "openai") {
		t.Fatalf("expected openai written back into cfg.Grants; got %v", cfg.Grants)
	}
	if !cfg.ShouldSyncCodexLogs() {
		t.Errorf("ShouldSyncCodexLogs() = false, want true once the derived openai grant is on cfg.Grants")
	}
}

// TestExpandAgentsWriteBackCompanionNoAgentsNoGrant is the companion to
// TestExpandAgentsWriteBackEnablesCodexLogSync: a config with neither
// `agents:` nor an explicit openai grant must still report false — the fix
// must not make ShouldSyncCodexLogs default to true.
func TestExpandAgentsWriteBackCompanionNoAgentsNoGrant(t *testing.T) {
	cfg := &config.Config{}
	derived, err := cli.ExpandAgents(cfg)
	if err != nil {
		t.Fatalf("ExpandAgents: %v", err)
	}
	cli.AppendDerivedGrants(cfg, derived)
	if cfg.ShouldSyncCodexLogs() {
		t.Errorf("ShouldSyncCodexLogs() = true, want false with no agents: and no openai grant; cfg.Grants = %v", cfg.Grants)
	}
}

func TestExpandAgentsRejectsBadEntries(t *testing.T) {
	tests := []struct {
		name   string
		agents []string
		errHas string
	}{
		{"unknown name", []string{"vibrant-code"}, "vibrant-code"},
		{"non-agent provider", []string{"github"}, "github"},
		{"agent without AgentRuntime", []string{"pi"}, "pi"},
		{"empty string entry", []string{""}, "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Agents: tt.agents}
			_, err := cli.ExpandAgents(cfg)
			if err == nil {
				t.Fatal("expected a hard error — a dropped entry silently costs a credential and firewall rules")
			}
			if !strings.Contains(err.Error(), tt.errHas) {
				t.Errorf("error %q should name %q", err, tt.errHas)
			}
		})
	}
}

func countOccurrences(list []string, want string) int {
	n := 0
	for _, s := range list {
		if s == want || strings.HasPrefix(s, want+"@") {
			n++
		}
	}
	return n
}

func TestAgentFieldReachesDegradationSites(t *testing.T) {
	// isAIAgent is the cheapest observable proxy for the five HasPrefix call
	// sites in manager_create.go that a bogus agent: silently disabled.
	cfg := &config.Config{Agent: "vibrant-code"}
	cli.ResolveAgentField(cfg, "claude")
	if !strings.HasPrefix(cfg.Agent, "claude") {
		t.Errorf("agent %q must satisfy the HasPrefix checks that gate memory "+
			"limits, implied deps, and language servers", cfg.Agent)
	}

	// Companion: moat run with no verb and no valid field leaves it empty, and
	// those sites correctly stay off rather than matching something wrong.
	bare := &config.Config{Agent: "vibrant-code"}
	cli.ResolveAgentField(bare, "")
	if bare.Agent != "" {
		t.Errorf("cfg.Agent = %q, want empty so the HasPrefix sites stay off", bare.Agent)
	}
}

// TestResolveAgentFieldCanonicalizes covers the alias half of the degradation
// this file exists to prevent. KnownAgentNames advertises the registry aliases
// (openai, google) as valid `agent:` values and ValidateAgent accepts them, so
// they must also reach cfg.Agent in the spelling the downstream
// strings.HasPrefix consumers in manager_create.go match against. "openai"
// prefix-matches none of claude/codex/copilot/gemini/pi; "codex" does.
func TestResolveAgentFieldCanonicalizes(t *testing.T) {
	tests := []struct {
		name   string
		agent  string
		agents []string
		verb   string
		want   string
	}{
		{"alias in agent: resolves to the provider name", "openai", nil, "", "codex"},
		{"google resolves to gemini", "google", nil, "", "gemini"},
		{"alias via the agents[0] backfill", "", []string{"openai"}, "", "codex"},
		{"documented variant collapses too", "claude-code", nil, "", "claude"},
		{"variant in agents[0] collapses", "", []string{"claude-code"}, "", "claude"},
		// Companion: the verb path already assigns a canonical provider name,
		// so canonicalization must leave it exactly as it was.
		{"verb value is already canonical", "openai", nil, "claude", "claude"},
		// Companion: an unknown value is still cleared, not "canonicalized"
		// into some nearby agent.
		{"unknown stays cleared", "vibrant-code", nil, "", ""},
		// Companion: no agent info at all stays empty.
		{"empty stays empty", "", nil, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			orig := ui.Writer()
			ui.SetWriter(&buf)
			t.Cleanup(func() { ui.SetWriter(orig) })

			cfg := &config.Config{Agent: tt.agent, Agents: tt.agents}
			cli.ResolveAgentField(cfg, tt.verb)

			if cfg.Agent != tt.want {
				t.Errorf("cfg.Agent = %q, want %q", cfg.Agent, tt.want)
			}
		})
	}
}

// TestAliasAgentReachesDegradationSites is the companion to
// TestAgentFieldReachesDegradationSites: that one proves a hallucinated value
// is repaired by the verb, this one proves an alias moat itself advertises as
// valid ends up satisfying the same HasPrefix gates. Before canonicalization
// both `agent: openai` and `agents: [openai]` passed validation and then
// silently switched off the memory limit, implied deps, and language-server
// support.
func TestAliasAgentReachesDegradationSites(t *testing.T) {
	canonicalPrefixes := []string{"claude", "codex", "copilot", "gemini", "pi"}
	satisfiesPrefixGate := func(agent string) bool {
		for _, p := range canonicalPrefixes {
			if strings.HasPrefix(agent, p) {
				return true
			}
		}
		return false
	}

	for _, alias := range []string{"openai", "google"} {
		t.Run("agent field "+alias, func(t *testing.T) {
			cfg := &config.Config{Agent: alias}
			cli.ResolveAgentField(cfg, "")
			if !satisfiesPrefixGate(cfg.Agent) {
				t.Errorf("agent: %s resolved to %q, which matches none of the "+
					"HasPrefix gates in manager_create.go", alias, cfg.Agent)
			}
		})
		t.Run("agents list "+alias, func(t *testing.T) {
			cfg := &config.Config{Agents: []string{alias}}
			if _, err := cli.ExpandAgents(cfg); err != nil {
				t.Fatalf("ExpandAgents: %v", err)
			}
			cli.ResolveAgentField(cfg, "")
			if !satisfiesPrefixGate(cfg.Agent) {
				t.Errorf("agents: [%s] backfilled %q, which matches none of the "+
					"HasPrefix gates in manager_create.go", alias, cfg.Agent)
			}
		})
	}
}

// TestAgentDocTagListsEveryKnownAgentName guards the two "valid values" lists
// against drift. The `doc:` tag on config.Config.Agent is rendered into moat
// init's LLM prompt (quickstart.GenerateSchemaReference), while
// KnownAgentNames() produces the list in the runtime warning — a value named by
// one and not the other is either an unadvertised accepted value or, worse, a
// prompt telling the model to write something moat rejects.
func TestAgentDocTagListsEveryKnownAgentName(t *testing.T) {
	field, ok := reflect.TypeOf(config.Config{}).FieldByName("Agent")
	if !ok {
		t.Fatal("config.Config has no Agent field")
	}
	doc := field.Tag.Get("doc")
	if doc == "" {
		t.Fatal("config.Config.Agent has no doc tag; moat init's prompt would document it as a bare string")
	}
	for _, name := range cli.KnownAgentNames() {
		if !strings.Contains(doc, name) {
			t.Errorf("KnownAgentNames() accepts %q but the Agent doc tag never mentions it: %q", name, doc)
		}
	}
}

// runProviderDryRun drives RunProvider against a workspace containing the given
// moat.yaml with DryRun set, and returns everything written to the ui writer.
// The verb is "codex" so ResolveAgentField takes the real provider path.
func runProviderDryRun(t *testing.T, moatYAML string) string {
	t.Helper()

	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "moat.yaml"), []byte(moatYAML), 0o600); err != nil {
		t.Fatalf("writing moat.yaml: %v", err)
	}

	oldDryRun := cli.DryRun
	cli.DryRun = true
	t.Cleanup(func() { cli.DryRun = oldDryRun })

	var buf bytes.Buffer
	orig := ui.Writer()
	ui.SetWriter(&buf)
	t.Cleanup(func() { ui.SetWriter(orig) })

	cmd := &cobra.Command{
		Use:           "codex",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, a []string) error {
			return cli.RunProvider(c, a, cli.ProviderRunConfig{
				Name:         "codex",
				Flags:        &cli.ExecFlags{},
				BuildCommand: func(_, _ string) ([]string, error) { return []string{"noop"}, nil },
			})
		},
	}
	cmd.SetArgs([]string{ws})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return buf.String()
}

// TestDryRunStillValidatesAgentField is a regression test for the ordering of
// resolveProviderAgentField relative to the DryRun return. --dry-run is what
// someone reaches for to check a moat.yaml before committing to a run, so it
// is the worst place to skip the very warning that tells them `agent:` is
// wrong. The call used to sit after the dry-run return and never fired.
func TestDryRunStillValidatesAgentField(t *testing.T) {
	out := runProviderDryRun(t, "agent: vibrant-code\n")
	if !strings.Contains(out, "not a known agent") {
		t.Errorf("dry run should warn about an unknown agent: value; got %q", out)
	}

	// Companion: a valid agent: under the same dry-run path stays silent, so
	// the test above is detecting the bad value rather than a warning that
	// fires unconditionally.
	if out := runProviderDryRun(t, "agent: codex\n"); out != "" {
		t.Errorf("dry run with a valid agent: should warn about nothing; got %q", out)
	}
}
