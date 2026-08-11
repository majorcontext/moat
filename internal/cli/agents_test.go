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
