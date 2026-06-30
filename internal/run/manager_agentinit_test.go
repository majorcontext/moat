package run

import (
	"context"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
)

func TestSetupCodexStaging_UnknownGrant(t *testing.T) {
	m := &Manager{}
	cfg := &config.Config{}
	cfg.Codex.MCP = map[string]config.MCPServerSpec{"srv": {Grant: "bogus"}}
	// The provider is nil here: grant validation errors return before
	// PrepareContainer is ever called.
	_, err := m.setupCodexStaging(context.Background(), nil, Options{Config: cfg}, false, "", "", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown grant") {
		t.Fatalf("expected unknown-grant error, got %v", err)
	}
}

func TestSetupCodexStaging_GrantNotDeclared(t *testing.T) {
	m := &Manager{}
	cfg := &config.Config{}
	// "github" is a known grant but is not in the top-level grants list.
	cfg.Codex.MCP = map[string]config.MCPServerSpec{"srv": {Grant: "github"}}
	_, err := m.setupCodexStaging(context.Background(), nil, Options{Config: cfg}, false, "", "", nil)
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("expected grant-not-declared error, got %v", err)
	}
}

func TestBuildLocalMCPConfig_NoSpecs(t *testing.T) {
	out, err := buildLocalMCPConfig("codex", nil, nil)
	if err != nil || out != nil {
		t.Fatalf("expected (nil, nil) for no specs, got (%v, %v)", out, err)
	}
}

func TestBuildLocalMCPConfig_NoGrant(t *testing.T) {
	specs := map[string]config.MCPServerSpec{"srv": {Command: "run", Args: []string{"-x"}, Cwd: "/w"}}
	out, err := buildLocalMCPConfig("codex", specs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := out["srv"]
	if !ok || got.Command != "run" || len(got.Args) != 1 || got.Cwd != "/w" {
		t.Fatalf("spec not converted faithfully: %+v", out)
	}
	if got.Env != nil {
		t.Fatalf("no grant means no env injection, got %v", got.Env)
	}
}

func TestBuildLocalMCPConfig_GrantInjectsEnvWithoutMutatingSpec(t *testing.T) {
	orig := map[string]string{"EXISTING": "1"}
	specs := map[string]config.MCPServerSpec{"srv": {Grant: "github", Env: orig}}
	out, err := buildLocalMCPConfig("codex", specs, []string{"github"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	env := out["srv"].Env
	if env["EXISTING"] != "1" || len(env) != 2 {
		t.Fatalf("expected the existing env plus one injected placeholder, got %v", env)
	}
	if len(orig) != 1 {
		t.Fatalf("the original spec env must not be mutated, got %v", orig)
	}
}

func TestSetupGeminiStaging_UnknownGrant(t *testing.T) {
	m := &Manager{}
	cfg := &config.Config{}
	cfg.Gemini.MCP = map[string]config.MCPServerSpec{"srv": {Grant: "bogus"}}
	_, err := m.setupGeminiStaging(context.Background(), nil, Options{Config: cfg}, false, "", "", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown grant") {
		t.Fatalf("expected unknown-grant error, got %v", err)
	}
}

func TestSetupGeminiStaging_GrantNotDeclared(t *testing.T) {
	m := &Manager{}
	cfg := &config.Config{}
	// "github" is a known grant but is not in the top-level grants list.
	cfg.Gemini.MCP = map[string]config.MCPServerSpec{"srv": {Grant: "github"}}
	_, err := m.setupGeminiStaging(context.Background(), nil, Options{Config: cfg}, false, "", "", nil)
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("expected grant-not-declared error, got %v", err)
	}
}
