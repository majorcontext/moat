package runctx

import (
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
)

func TestBuildFromConfig_anthropicShellScopedKey(t *testing.T) {
	cfg := &config.Config{
		Agent:  "claude",
		Grants: []string{"claude", "anthropic"},
	}

	rc := BuildFromConfig(cfg, "run-anthropic", BuildOptions{AnthropicKeyEnv: "ANTHROPIC_API_KEY"})

	if rc.AnthropicAPI == nil {
		t.Fatal("AnthropicAPI is nil, want non-nil")
	}
	if rc.AnthropicAPI.KeyEnv != "ANTHROPIC_API_KEY" {
		t.Errorf("AnthropicAPI.KeyEnv = %q, want %q", rc.AnthropicAPI.KeyEnv, "ANTHROPIC_API_KEY")
	}
}

// Companion: with the key exported container-wide (anthropic grant alone, or
// no anthropic grant at all) there is nothing unusual to explain, so the
// section must stay out of the rendered context.
func TestBuildFromConfig_noAnthropicSectionWhenKeyIsGlobal(t *testing.T) {
	cfg := &config.Config{
		Agent:  "claude",
		Grants: []string{"anthropic"},
	}

	rc := BuildFromConfig(cfg, "run-anthropic-only", BuildOptions{})

	if rc.AnthropicAPI != nil {
		t.Errorf("AnthropicAPI = %+v, want nil when AnthropicKeyEnv is unset", rc.AnthropicAPI)
	}
}

// `moat run --grant claude --grant anthropic` needs no moat.yaml, so cfg is
// nil on that documented path. The section is the guardrail that stops an agent
// from exporting the placeholder key globally, so it has to survive there.
func TestBuildFromConfig_nilConfigStillExplainsAnthropicKey(t *testing.T) {
	rc := BuildFromConfig(nil, "run-no-config", BuildOptions{AnthropicKeyEnv: "ANTHROPIC_API_KEY"})

	if rc == nil {
		t.Fatal("BuildFromConfig(nil, ...) = nil, want a context")
	}
	if rc.AnthropicAPI == nil {
		t.Fatal("AnthropicAPI is nil, want the section to render without a moat.yaml")
	}
	if !strings.Contains(Render(rc), "## Calling the Anthropic API") {
		t.Error("rendered context missing the Anthropic API section")
	}
}

// Companion: a nil config with nothing notable renders without panicking and
// without inventing sections.
func TestBuildFromConfig_nilConfigMinimalContext(t *testing.T) {
	rc := BuildFromConfig(nil, "run-bare", BuildOptions{})

	if rc == nil {
		t.Fatal("BuildFromConfig(nil, ...) = nil, want a context")
	}
	got := Render(rc)
	if !strings.Contains(got, "run-bare") {
		t.Errorf("rendered context missing the run ID:\n%s", got)
	}
	if strings.Contains(got, "## Calling the Anthropic API") {
		t.Error("did not expect the Anthropic API section for a bare run")
	}
	if strings.Contains(got, "- Agent: \n") {
		t.Error("empty Agent line should be omitted, not rendered blank")
	}
}

func TestRender_anthropicSection(t *testing.T) {
	rc := &RuntimeContext{
		RunID:        "run-anthropic",
		Agent:        "claude",
		Workspace:    "/workspace",
		AnthropicAPI: &AnthropicAPI{KeyEnv: "ANTHROPIC_API_KEY"},
	}

	got := Render(rc)

	if !strings.Contains(got, "## Calling the Anthropic API") {
		t.Fatal("expected the Anthropic API section")
	}
	// The three things an agent must know, or it will "helpfully" break the setup.
	for _, want := range []string{
		"placeholder",
		`-H "x-api-key: $ANTHROPIC_API_KEY"`,
		"Do not export it globally",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered context missing %q", want)
		}
	}
	// A 429 must be steered toward the credential path, not toward discovering
	// that a Claude Code system prompt makes OAuth work for Sonnet/Opus.
	if !strings.Contains(got, "429") {
		t.Error("expected the 429 explanation so agents don't go hunting for a workaround")
	}
}

// Companion: no AnthropicAPI, no section.
func TestRender_noAnthropicSectionWhenUnset(t *testing.T) {
	rc := &RuntimeContext{
		RunID:     "run-plain",
		Agent:     "claude",
		Workspace: "/workspace",
	}

	got := Render(rc)

	if strings.Contains(got, "## Calling the Anthropic API") {
		t.Error("did not expect the Anthropic API section when AnthropicAPI is nil")
	}
	if strings.Contains(got, "BASH_ENV") {
		t.Error("did not expect BASH_ENV to be mentioned when AnthropicAPI is nil")
	}
}
