package quickstart

import (
	"strings"
	"testing"
)

func TestGenerateSchemaReference(t *testing.T) {
	ref := GenerateSchemaReference()

	// Must start with the expected header.
	if !strings.HasPrefix(ref, "## moat.yaml Schema Reference") {
		t.Errorf("expected header, got:\n%s", ref[:min(len(ref), 100)])
	}

	// Key fields that must appear.
	requiredFields := []string{
		"`name`",
		"`agent`",
		"`dependencies`",
		"`grants`",
		"`env`",
		"`mounts`",
		"`ports`",
		"`network`",
		"`network.policy`",
		"`command`",
		"`hooks`",
		"`hooks.post_build`",
		"`interactive`",
		"`volumes`",
		"`container`",
		"`container.memory`",
		"`mcp`",
		"`claude`",
		"`codex`",
		"`gemini`",
	}

	for _, field := range requiredFields {
		if !strings.Contains(ref, field) {
			t.Errorf("missing expected field %s in output:\n%s", field, ref)
		}
	}

	// Verify type annotations are present.
	typeChecks := map[string]string{
		"`name` (string)":           "name should be string",
		"`interactive` (bool)":      "interactive should be bool",
		"`dependencies` ([]string)": "dependencies should be []string",
		"`network` (object)":        "network should be object",
	}
	for check, desc := range typeChecks {
		if !strings.Contains(ref, check) {
			t.Errorf("%s: expected %q in output", desc, check)
		}
	}

	// Fields with yaml:"-" must not appear.
	if strings.Contains(ref, "DeprecatedRuntime") {
		t.Error("deprecated runtime field should be excluded (yaml:\"-\")")
	}
}

func TestBuildPrompt(t *testing.T) {
	prompt := BuildPrompt(t.TempDir())

	// Must contain the schema reference section.
	if !strings.Contains(prompt, "moat.yaml Schema Reference") {
		t.Error("expected prompt to contain schema reference header")
	}

	// Must contain the deps reference sections.
	if !strings.Contains(prompt, "Available Dependencies") {
		t.Error("expected prompt to contain available dependencies header")
	}
	if !strings.Contains(prompt, "Dynamic Dependencies") {
		t.Error("expected prompt to contain dynamic dependencies header")
	}

	// Must contain task instruction.
	if !strings.Contains(prompt, "analyze the project") {
		t.Error("expected prompt to contain task instruction about analyzing the project")
	}

	// Must contain output instruction.
	if !strings.Contains(prompt, "Output only valid YAML") {
		t.Error("expected prompt to contain output instruction")
	}

	// Must contain examples.
	if !strings.Contains(prompt, "Example 1: Node.js web app") {
		t.Error("expected prompt to contain example 1")
	}
	if !strings.Contains(prompt, "Example 2: Python ML project") {
		t.Error("expected prompt to contain example 2")
	}
	if !strings.Contains(prompt, "Example 3: Go service with Redis") {
		t.Error("expected prompt to contain example 3")
	}

	// Size should be reasonable (under 20KB).
	if len(prompt) > 20*1024 {
		t.Errorf("prompt is too large: %d bytes (max 20KB)", len(prompt))
	}
}

func TestGenerateDepsReference(t *testing.T) {
	ref := GenerateDepsReference()

	// Must start with the expected header.
	if !strings.HasPrefix(ref, "## Available Dependencies") {
		t.Errorf("expected header, got:\n%s", ref[:min(len(ref), 100)])
	}

	// Key dependencies that must appear.
	requiredDeps := []string{
		"`node`",
		"`python`",
		"`go`",
		"`postgres`",
		"`redis`",
		"`claude-code`",
	}
	for _, dep := range requiredDeps {
		if !strings.Contains(ref, dep) {
			t.Errorf("missing expected dependency %s in output:\n%s", dep, ref)
		}
	}

	// Dynamic prefixes must appear.
	dynamicPrefixes := []string{
		"`npm:<package>`",
		"`pip:<package>`",
		"`uv:<package>`",
		"`cargo:<package>`",
		"`go:<package>`",
	}
	for _, prefix := range dynamicPrefixes {
		if !strings.Contains(ref, prefix) {
			t.Errorf("missing expected dynamic prefix %s in output:\n%s", prefix, ref)
		}
	}

	// Verify version and default info for a known dep.
	if !strings.Contains(ref, "(default: 20)") {
		t.Error("expected node default version (20)")
	}
	if !strings.Contains(ref, "[versions: 18, 20, 22, 24]") {
		t.Error("expected node version list")
	}

	// Verify the hooks section for system packages.
	if !strings.Contains(ref, "post_build_root") {
		t.Error("expected lifecycle hooks section with post_build_root")
	}

	// Verify Docker dependency section.
	if !strings.Contains(ref, "docker:host") {
		t.Error("expected docker:host in Docker dependency section")
	}
	if !strings.Contains(ref, "docker:dind") {
		t.Error("expected docker:dind in Docker dependency section")
	}
	if !strings.Contains(ref, "runtime: docker") {
		t.Error("expected runtime: docker requirement for Docker deps")
	}
}
