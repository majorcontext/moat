package deps

import (
	"regexp"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/providers/codex"
)

// Drift guard. The codex provider stages openai-env.sh into the init mount and
// points BASH_ENV at a copy in $HOME/.codex; this script is what makes that
// copy. If it stops happening, bash ignores the missing BASH_ENV file silently
// and shell commands never receive the OpenAI API key.
func TestMoatInitCopiesOpenAIShellEnv(t *testing.T) {
	want := `cp -p "$MOAT_CODEX_INIT/` + codex.OpenAIShellEnvFileName + `" "$TARGET_HOME/.codex/"`
	if !strings.Contains(MoatInitScript, want) {
		t.Errorf("moat-init.sh missing the copy step:\n  want a line containing: %s", want)
	}
	if !strings.HasSuffix(codex.OpenAIShellEnvPath, "/.codex/"+codex.OpenAIShellEnvFileName) {
		t.Errorf("OpenAIShellEnvPath = %q, but moat-init.sh copies into $TARGET_HOME/.codex/", codex.OpenAIShellEnvPath)
	}
}

// The in-container version gate aborts the whole entrypoint, so it must fire
// only for subscription auth. Staging also runs for API-key runs, local codex
// MCP servers, and log sync — including on images with no codex installed,
// where an unconditional gate killed runs that never touched Codex.
func TestMoatInitVersionGateIsScopedToSubscriptionAuth(t *testing.T) {
	block := codexInitBlock(t)
	gate := strings.Index(block, "MOAT_CODEX_SUBSCRIPTION_AUTH")
	if gate < 0 {
		t.Fatal("moat-init.sh gates the Codex version unconditionally; it must check MOAT_CODEX_SUBSCRIPTION_AUTH")
	}
	version := strings.Index(block, "codex --version")
	if version < 0 {
		t.Fatal("moat-init.sh no longer checks the Codex version")
	}
	if version < gate {
		t.Error("the version check runs before the subscription guard, so it applies to every Codex run")
	}
	if !strings.Contains(block, "command -v codex") {
		t.Error("the gate must check that codex exists before reading its version, or a missing binary reads as an unsupported version")
	}
}

// Parity guard: the shell case list and the Go validator encode the same
// supported range. They are edited in different files, and a mismatch means a
// run either passes the host check and dies at container start, or vice versa.
func TestMoatInitVersionRangeMatchesValidator(t *testing.T) {
	prefixes := regexp.MustCompile(`0\.\d+\.\*`).FindAllString(codexInitBlock(t), -1)
	if len(prefixes) == 0 {
		t.Fatal("no version prefixes found in moat-init.sh's Codex gate")
	}
	accepted := make(map[string]bool, len(prefixes))
	for _, prefix := range prefixes {
		version := strings.TrimSuffix(prefix, "*") + "0"
		accepted[strings.TrimSuffix(prefix, ".*")] = true
		if err := codex.ValidateVersion(version); err != nil {
			t.Errorf("moat-init.sh accepts %s but ValidateVersion rejects %s: %v", prefix, version, err)
		}
	}
	// And the other direction: a version the shell list excludes must also be
	// rejected by the validator.
	for _, version := range []string{"0.145.0", "0.155.0", "1.0.0"} {
		if accepted[version[:strings.LastIndex(version, ".")]] {
			continue
		}
		if err := codex.ValidateVersion(version); err == nil {
			t.Errorf("ValidateVersion accepts %s but moat-init.sh's case list does not", version)
		}
	}
}

// codexInitBlock returns the MOAT_CODEX_INIT section of the init script.
func codexInitBlock(t *testing.T) string {
	t.Helper()
	const start = `if [ -n "$MOAT_CODEX_INIT" ]`
	i := strings.Index(MoatInitScript, start)
	if i < 0 {
		t.Fatal("moat-init.sh has no MOAT_CODEX_INIT block")
	}
	rest := MoatInitScript[i:]
	if j := strings.Index(rest, "\nfi\n"); j > 0 {
		return rest[:j]
	}
	return rest
}
