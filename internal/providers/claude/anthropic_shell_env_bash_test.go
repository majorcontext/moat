package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exercise the actual bash semantics the design depends on:
// non-interactive bash sources BASH_ENV, and the claude() guard strips the key
// before handing off. If either assumption breaks, the scoping silently stops
// working — Claude Code would either lose the key it should never have, or keep
// one it must not see.
func writeShellEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, AnthropicShellEnvFileName)
	if err := os.WriteFile(p, []byte(RenderAnthropicShellEnv()), 0o644); err != nil {
		t.Fatalf("writing shell env file: %v", err)
	}
	return p
}

// runBash runs script under a non-interactive bash with BASH_ENV set, exactly
// as Claude Code's Bash tool invokes commands (`bash -c '...'`).
func runBash(t *testing.T, bashEnv, script string, extraPath string) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	cmd := exec.Command(bash, "-c", script)
	env := []string{"BASH_ENV=" + bashEnv, "PATH=" + extraPath + ":" + os.Getenv("PATH")}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash script failed: %v\noutput: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestAnthropicShellEnv_bashExportsKey(t *testing.T) {
	got := runBash(t, writeShellEnv(t), `printf '%s' "$ANTHROPIC_API_KEY"`, "")
	if got != ProxyInjectedPlaceholder {
		t.Errorf("ANTHROPIC_API_KEY in bash = %q, want %q", got, ProxyInjectedPlaceholder)
	}
}

// fakeClaude installs a non-bash "claude" on PATH. It must not be a shell
// script: a bash-script launcher would re-source BASH_ENV and re-export the
// key, which is not how the real (native binary) claude behaves.
func fakeClaude(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/usr/bin/env python3\n" +
		"import os\n" +
		"print(os.environ.get('ANTHROPIC_API_KEY', ''))\n"
	p := filepath.Join(dir, "claude")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake claude: %v", err)
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	return dir
}

func TestAnthropicShellEnv_guardStripsKeyFromNestedClaude(t *testing.T) {
	got := runBash(t, writeShellEnv(t), `claude`, fakeClaude(t))
	if got != "" {
		t.Errorf("nested claude saw ANTHROPIC_API_KEY = %q, want it stripped", got)
	}
}

// The guard must also hold one level down, where a script re-sources BASH_ENV
// and the function is redefined.
func TestAnthropicShellEnv_guardHoldsInNestedBashScript(t *testing.T) {
	dir := t.TempDir()
	inner := filepath.Join(dir, "inner.sh")
	if err := os.WriteFile(inner, []byte("claude\n"), 0o755); err != nil {
		t.Fatalf("writing inner script: %v", err)
	}
	got := runBash(t, writeShellEnv(t), "bash "+inner, fakeClaude(t))
	if got != "" {
		t.Errorf("claude in nested bash script saw ANTHROPIC_API_KEY = %q, want it stripped", got)
	}
}

// Ordinary commands must still get the key — that is the entire point.
func TestAnthropicShellEnv_ordinaryCommandsKeepKey(t *testing.T) {
	got := runBash(t, writeShellEnv(t), `bash -c 'printf "%s" "$ANTHROPIC_API_KEY"'`, "")
	if got != ProxyInjectedPlaceholder {
		t.Errorf("nested shell saw ANTHROPIC_API_KEY = %q, want %q", got, ProxyInjectedPlaceholder)
	}
}
