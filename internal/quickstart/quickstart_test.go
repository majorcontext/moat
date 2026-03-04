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
	if !strings.Contains(ref, "[versions: 18, 20, 22]") {
		t.Error("expected node version list")
	}

	// Verify the hooks section for system packages.
	if !strings.Contains(ref, "post_build_root") {
		t.Error("expected lifecycle hooks section with post_build_root")
	}
}
