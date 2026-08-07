package deps

import (
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/providers/claude"
)

// Drift guard. The claude provider stages anthropic-env.sh into the init mount
// and points BASH_ENV at a copy in $HOME/.claude; this script is what makes
// that copy. Nothing else ties the two together, and if the copy silently stops
// happening bash ignores the missing BASH_ENV file without error — shell
// commands would just never receive the Anthropic API key.
func TestMoatInitCopiesAnthropicShellEnv(t *testing.T) {
	if !strings.Contains(MoatInitScript, claude.AnthropicShellEnvFileName) {
		t.Fatalf("moat-init.sh does not mention %q — BASH_ENV would point at a file that is never created", claude.AnthropicShellEnvFileName)
	}

	want := `cp -p "$MOAT_CLAUDE_INIT/` + claude.AnthropicShellEnvFileName + `" "$TARGET_HOME/.claude/"`
	if !strings.Contains(MoatInitScript, want) {
		t.Errorf("moat-init.sh missing the copy step:\n  want a line containing: %s", want)
	}
}

// The copy must land in the directory BASH_ENV resolves to. If the provider's
// path moves without the script following, the key silently stops arriving.
func TestMoatInitAnthropicEnvTargetMatchesProviderPath(t *testing.T) {
	if !strings.HasSuffix(claude.AnthropicShellEnvPath, "/.claude/"+claude.AnthropicShellEnvFileName) {
		t.Errorf("AnthropicShellEnvPath = %q, but moat-init.sh copies into $TARGET_HOME/.claude/", claude.AnthropicShellEnvPath)
	}
}
