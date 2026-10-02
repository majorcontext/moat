package moatinit

import (
	"fmt"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/providers/claude"
	"github.com/majorcontext/moat/internal/providers/codex"
)

// These drift guards replace the shell-text guards main added to
// internal/deps before the entrypoint moved to Go. They assert the Go phases
// against the provider constants that stage the files, so a rename on either
// side fails here instead of silently dropping a runtime credential.

// The claude provider stages anthropic-env.sh into the init mount and points
// BASH_ENV at a copy in $HOME/.claude; the entrypoint phase is what makes that
// copy. If it stops happening bash ignores the missing BASH_ENV file without
// error and shell commands never receive the Anthropic API key.
func TestClaudeStagingCopiesProviderShellEnv(t *testing.T) {
	if !strings.HasSuffix(claude.AnthropicShellEnvPath, "/.claude/"+claude.AnthropicShellEnvFileName) {
		t.Fatalf("AnthropicShellEnvPath = %q, but the entrypoint stages into $TARGET_HOME/.claude/", claude.AnthropicShellEnvPath)
	}
	ts := newTestSys(t, 0, true)
	staging := stageFile(t, ts, "mnt/claude-init", claude.AnthropicShellEnvFileName, 0o644, "export ANTHROPIC_API_KEY=x")
	ctx, _ := newTestContext(ts, Config{ClaudeInit: staging, Home: "/root"})
	if err := claudeStagingPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if !exists(ts, "/home/moatuser/.claude/"+claude.AnthropicShellEnvFileName) {
		t.Fatalf("claude staging did not copy %q — BASH_ENV would point at a file that is never created", claude.AnthropicShellEnvFileName)
	}
}

// Companion: the codex provider's OpenAI BASH_ENV file must be staged into
// $HOME/.codex the same way.
func TestCodexStagingCopiesProviderShellEnv(t *testing.T) {
	if !strings.HasSuffix(codex.OpenAIShellEnvPath, "/.codex/"+codex.OpenAIShellEnvFileName) {
		t.Fatalf("OpenAIShellEnvPath = %q, but the entrypoint stages into $TARGET_HOME/.codex/", codex.OpenAIShellEnvPath)
	}
	ts := newTestSys(t, 0, true)
	staging := stageFile(t, ts, "mnt/codex-init", codex.OpenAIShellEnvFileName, 0o600, "export OPENAI_API_KEY=x")
	ctx, _ := newTestContext(ts, Config{CodexInit: staging, Home: "/root"})
	if err := codexStagingPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if !exists(ts, "/home/moatuser/.codex/"+codex.OpenAIShellEnvFileName) {
		t.Fatalf("codex staging did not copy %q — BASH_ENV would point at a file that is never created", codex.OpenAIShellEnvFileName)
	}
}

// Parity guard: the entrypoint's accepted Codex range and the host-side
// validator encode the same supported set. A mismatch means a run either
// passes the host check and dies at container start, or vice versa.
func TestCodexVersionRangeMatchesValidator(t *testing.T) {
	for minor := 140; minor <= 160; minor++ {
		v := fmt.Sprintf("0.%d.0", minor)
		if got, want := supportedCodexVersion(v), codex.ValidateVersion(v) == nil; got != want {
			t.Errorf("supportedCodexVersion(%q) = %v, but codex.ValidateVersion agrees = %v", v, got, want)
		}
	}
	for _, v := range []string{"", "unknown", "1.0.0", "0.146", "codex-cli"} {
		if supportedCodexVersion(v) {
			t.Errorf("supportedCodexVersion(%q) = true, want false", v)
		}
		if codex.ValidateVersion(v) == nil {
			t.Errorf("codex.ValidateVersion(%q) = nil, want error", v)
		}
	}
}
