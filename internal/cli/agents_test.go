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
	"slices"
	"strings"
	"testing"

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
	for _, want := range []string{"claude", "claude-code", "codex"} {
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

func TestExpandAgents(t *testing.T) {
	cfg := &config.Config{Agents: []string{"claude", "codex"}}
	if err := cli.ExpandAgents(cfg); err != nil {
		t.Fatalf("ExpandAgents: %v", err)
	}

	for _, dep := range []string{"claude-code", "codex-cli"} {
		if !cli.HasDependency(cfg.Dependencies, dep) {
			t.Errorf("expected dependency %q; got %v", dep, cfg.Dependencies)
		}
	}
	// codex's grant is openai, not codex.
	if !slices.Contains(cfg.Grants, "openai") {
		t.Errorf("expected grant openai; got %v", cfg.Grants)
	}
	if slices.Contains(cfg.Grants, "codex") {
		t.Errorf("codex must expand to the openai grant, not codex; got %v", cfg.Grants)
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
	if err := cli.ExpandAgents(cfg); err != nil {
		t.Fatalf("ExpandAgents: %v", err)
	}
	if got := countOccurrences(cfg.Dependencies, "claude-code"); got != 1 {
		t.Errorf("claude-code appears %d times, want 1: %v", got, cfg.Dependencies)
	}
	if got := countOccurrences(cfg.Grants, "claude"); got != 1 {
		t.Errorf("claude grant appears %d times, want 1: %v", got, cfg.Grants)
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
			err := cli.ExpandAgents(cfg)
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
