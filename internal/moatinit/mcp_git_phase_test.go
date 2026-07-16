package moatinit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceMCPJSONPhase(t *testing.T) {
	ts := newTestSys(t, 0, true)
	if err := os.MkdirAll(filepath.Join(ts.Root, "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	staging := stageFile(t, ts, "mnt/codex-init", "mcp.json", 0o644, `{"mcpServers":{}}`)

	ctx, _ := newTestContext(ts, Config{CodexInit: staging, Home: "/root"})
	if err := workspaceMCPJSONPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fileContent(t, ts, "/workspace/.mcp.json"); got != `{"mcpServers":{}}` {
		t.Errorf(".mcp.json = %q", got)
	}
	if got := statMode(t, ts, "/workspace/.mcp.json"); got != 0o644 {
		t.Errorf(".mcp.json mode = %o, want 644 preserved", got)
	}
	if !ts.chowned("/workspace/.mcp.json") {
		t.Error("no chown recorded for .mcp.json on the root path")
	}
}

func TestWorkspaceMCPJSONPhaseCompanions(t *testing.T) {
	// No staging vars set: no-op.
	ts := newTestSys(t, 0, true)
	ctx, _ := newTestContext(ts, Config{Home: "/root"})
	if err := workspaceMCPJSONPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if exists(ts, "/workspace/.mcp.json") {
		t.Error(".mcp.json created with no staging config")
	}

	// Staging set but no mcp.json file: no-op (INIT-12 companion).
	ts2 := newTestSys(t, 0, true)
	if err := os.MkdirAll(filepath.Join(ts2.Root, "mnt/gemini-init"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx2, _ := newTestContext(ts2, Config{GeminiInit: "/mnt/gemini-init", Home: "/root"})
	if err := workspaceMCPJSONPhase(ctx2); err != nil {
		t.Fatal(err)
	}
	if exists(ts2, "/workspace/.mcp.json") {
		t.Error(".mcp.json created without a staged mcp.json")
	}
}

func TestGitConfigPhaseRunsCommandsInOrder(t *testing.T) {
	ts := newTestSys(t, 0, true)
	ctx, _ := newTestContext(ts, Config{
		GitUserName:  "Ada Lovelace",
		GitUserEmail: "ada@example.com",
		GitSSHGitHub: "1",
		Home:         "/root",
	})
	if err := gitConfigPhase(ctx); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(ts.runs))
	for _, c := range ts.runs {
		got = append(got, strings.Join(c.Argv, " "))
	}
	want := []string{
		"git config --system --add safe.directory /workspace",
		"git config --system user.name Ada Lovelace",
		"git config --system user.email ada@example.com",
		"git config --system http.proxyAuthMethod basic",
		"git config --system url.git@github.com:.insteadOf https://github.com/",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestGitConfigPhaseSkipsWithoutGit(t *testing.T) {
	// GIT-01: no git binary, no commands, no error.
	ts := newTestSys(t, 0, true)
	ts.missingBinaries["git"] = true
	ctx, _ := newTestContext(ts, Config{GitUserName: "Ada", Home: "/root"})
	if err := gitConfigPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if len(ts.runs) != 0 {
		t.Errorf("git commands ran without a git binary: %v", ts.runs)
	}
}

func TestGitConfigPhaseBestEffortFailures(t *testing.T) {
	// GIT-07: git config failures (read-only /etc/gitconfig, non-root) do
	// not abort the phase.
	ts := newTestSys(t, 1000, false)
	ts.runHook = func(Cmd) (int, error) { return 1, nil }
	ctx, _ := newTestContext(ts, Config{Home: "/tmp/h"})
	if err := gitConfigPhase(ctx); err != nil {
		t.Fatalf("failing git config aborted the phase: %v", err)
	}
	if len(ts.runs) != 2 {
		t.Errorf("expected both unconditional commands to still run, got %d", len(ts.runs))
	}
}

// TestGitConfigPhaseRealGit exercises the phase against a real git binary
// with GIT_CONFIG_SYSTEM pointed at a temp file, proving the assembled
// argv actually produces the documented config.
func TestGitConfigPhaseRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	sysCfg := filepath.Join(t.TempDir(), "gitconfig")
	ts := newTestSys(t, 0, true)
	ts.runHook = func(c Cmd) (int, error) {
		cmd := exec.Command(c.Argv[0], c.Argv[1:]...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_SYSTEM="+sysCfg)
		if err := cmd.Run(); err != nil {
			return 1, nil
		}
		return 0, nil
	}
	ctx, _ := newTestContext(ts, Config{GitSSHGitHub: "1", GitUserName: "Ada", Home: "/root"})
	if err := gitConfigPhase(ctx); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(sysCfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(out)
	for _, want := range []string{"[safe]", "directory = /workspace", "proxyAuthMethod = basic", "insteadOf = https://github.com/", "name = Ada"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("system gitconfig missing %q:\n%s", want, cfg)
		}
	}
}
