package moatinit

import (
	"os"
	"path/filepath"
	"testing"
)

// stage writes a file into a host-side staging dir (absolute, outside the
// injected root — staging mounts are read through the same seam, so place
// them inside the root for the test).
func stageFile(t *testing.T, ts *testSys, stagingRel, name string, mode os.FileMode, content string) string {
	t.Helper()
	dir := filepath.Join(ts.Root, stagingRel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	// Explicit chmod: WriteFile honors umask at creation.
	if err := os.Chmod(filepath.Join(dir, name), mode); err != nil {
		t.Fatal(err)
	}
	return "/" + stagingRel
}

func statMode(t *testing.T, ts *testSys, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(filepath.Join(ts.Root, filepath.FromSlash(path[1:])))
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

func fileContent(t *testing.T, ts *testSys, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ts.Root, filepath.FromSlash(path[1:])))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func exists(ts *testSys, path string) bool {
	_, err := os.Lstat(filepath.Join(ts.Root, filepath.FromSlash(path[1:])))
	return err == nil
}

func TestAgentStagingFullSet(t *testing.T) {
	// (renamed from TestClaudeStagingFullSet when the env-shell files were added)
	ts := newTestSys(t, 0, true)
	staging := stageFile(t, ts, "mnt/claude-init", "settings.json", 0o640, `{"s":1}`)
	stageFile(t, ts, "mnt/claude-init", ".credentials.json", 0o644, `{"token":"x"}`)
	stageFile(t, ts, "mnt/claude-init", "remote-settings.json", 0o644, `{"r":1}`)
	stageFile(t, ts, "mnt/claude-init", "stats-cache.json", 0o644, `{}`)
	stageFile(t, ts, "mnt/claude-init", "CLAUDE.md", 0o644, "ctx")
	stageFile(t, ts, "mnt/claude-init", ".claude.json", 0o644, `{"onboarded":true}`)
	stageFile(t, ts, "mnt/claude-init", "anthropic-env.sh", 0o644, "export ANTHROPIC_API_KEY=x")
	stageFile(t, ts, "mnt/claude-init/statsig", "cache.db", 0o600, "st")
	// Allowlist companions: strays must NOT be copied.
	stageFile(t, ts, "mnt/claude-init", "stray.txt", 0o644, "no")
	stageFile(t, ts, "mnt/claude-init", "mcp.json", 0o644, "no")

	ctx, _ := newTestContext(ts, Config{ClaudeInit: staging, Home: "/root"})
	if err := claudeStagingPhase(ctx); err != nil {
		t.Fatal(err)
	}

	// Root+moatuser: TARGET_HOME is the hardcoded /home/moatuser.
	if got := fileContent(t, ts, "/home/moatuser/.claude/settings.json"); got != `{"s":1}` {
		t.Errorf("settings.json = %q", got)
	}
	// Non-secret modes preserved (cp -p).
	if got := statMode(t, ts, "/home/moatuser/.claude/settings.json"); got != 0o640 {
		t.Errorf("settings.json mode = %o, want 640 (preserved)", got)
	}
	// The four-secret contract: 0600 regardless of source mode.
	for _, p := range []string{"/home/moatuser/.claude/.credentials.json", "/home/moatuser/.claude/remote-settings.json"} {
		if got := statMode(t, ts, p); got != 0o600 {
			t.Errorf("%s mode = %o, want 600", p, got)
		}
	}
	// statsig dir copied recursively with modes preserved.
	if got := statMode(t, ts, "/home/moatuser/.claude/statsig/cache.db"); got != 0o600 {
		t.Errorf("statsig/cache.db mode = %o", got)
	}
	// .claude.json lands at the HOME ROOT, not inside .claude/.
	if !exists(ts, "/home/moatuser/.claude.json") {
		t.Error(".claude.json missing from home root")
	}
	// The BASH_ENV file is copied into .claude/ so $HOME/.claude/anthropic-env.sh
	// exists at runtime (non-secret: source mode preserved).
	if got := statMode(t, ts, "/home/moatuser/.claude/anthropic-env.sh"); got != 0o644 {
		t.Errorf("anthropic-env.sh mode = %o, want 644 preserved", got)
	}
	if exists(ts, "/home/moatuser/.claude/.claude.json") {
		t.Error(".claude.json wrongly copied into .claude/")
	}
	// Allowlist: strays absent.
	for _, p := range []string{"/home/moatuser/.claude/stray.txt", "/home/moatuser/.claude/mcp.json"} {
		if exists(ts, p) {
			t.Errorf("stray file leaked: %s", p)
		}
	}
	// Ownership hand-off recorded: the dir tree recursively (via lchown)
	// plus the home-root .claude.json.
	if !ts.chowned("/home/moatuser/.claude/.credentials.json") {
		t.Error("no chown recorded for .credentials.json")
	}
	if !ts.chowned("/home/moatuser/.claude.json") {
		t.Error("no chown recorded for home-root .claude.json")
	}
}

func TestAgentStagingGates(t *testing.T) {
	// Gate companions: unset var, empty dir path, and file-not-dir all skip.
	ts := newTestSys(t, 0, true)
	ctx, _ := newTestContext(ts, Config{Home: "/root"})
	if err := claudeStagingPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if exists(ts, "/home/moatuser/.claude") {
		t.Error(".claude created with no MOAT_CLAUDE_INIT")
	}

	// A file (not a directory) as the staging path skips the block.
	ts2 := newTestSys(t, 0, true)
	stageFile(t, ts2, "mnt", "notadir", 0o644, "x")
	ctx2, _ := newTestContext(ts2, Config{ClaudeInit: "/mnt/notadir", Home: "/root"})
	if err := claudeStagingPhase(ctx2); err != nil {
		t.Fatal(err)
	}
	if exists(ts2, "/home/moatuser/.claude") {
		t.Error(".claude created for file-typed staging path")
	}

	// Companion: an empty staging DIR still creates the agent dir.
	ts3 := newTestSys(t, 0, true)
	if err := os.MkdirAll(filepath.Join(ts3.Root, "mnt/claude-init"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx3, _ := newTestContext(ts3, Config{ClaudeInit: "/mnt/claude-init", Home: "/root"})
	if err := claudeStagingPhase(ctx3); err != nil {
		t.Fatal(err)
	}
	if !exists(ts3, "/home/moatuser/.claude") {
		t.Error(".claude not created for empty staging dir")
	}
}

func TestAgentStagingNonRootTargetsHome(t *testing.T) {
	ts := newTestSys(t, 1000, true) // moatuser exists but we're not root
	staging := stageFile(t, ts, "mnt/codex-init", "config.toml", 0o644, "cfg")
	stageFile(t, ts, "mnt/codex-init", "auth.json", 0o644, `{"k":"v"}`)
	ctx, _ := newTestContext(ts, Config{CodexInit: staging, Home: "/tmp/h"})
	if err := codexStagingPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if !exists(ts, "/tmp/h/.codex/config.toml") {
		t.Error("config.toml not staged under $HOME")
	}
	if got := statMode(t, ts, "/tmp/h/.codex/auth.json"); got != 0o600 {
		t.Errorf("auth.json mode = %o, want 600", got)
	}
	// Non-root: no chown of any kind.
	if len(ts.chowns) != 0 {
		t.Errorf("non-root path recorded chowns: %v", ts.chowns)
	}
}

func TestGeminiSettingsModePreserved(t *testing.T) {
	// AGENT-GEMINI-CP-SETTINGS: settings.json keeps its source mode — NOT
	// forced to 600 (companion to the oauth_creds.json secret contract).
	ts := newTestSys(t, 0, true)
	staging := stageFile(t, ts, "mnt/gemini-init", "settings.json", 0o640, `{}`)
	stageFile(t, ts, "mnt/gemini-init", "oauth_creds.json", 0o644, `{}`)
	ctx, _ := newTestContext(ts, Config{GeminiInit: staging, Home: "/root"})
	if err := geminiStagingPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if got := statMode(t, ts, "/home/moatuser/.gemini/settings.json"); got != 0o640 {
		t.Errorf("settings.json mode = %o, want 640 preserved", got)
	}
	if got := statMode(t, ts, "/home/moatuser/.gemini/oauth_creds.json"); got != 0o600 {
		t.Errorf("oauth_creds.json mode = %o, want 600", got)
	}
}

func TestCopilotStaging(t *testing.T) {
	ts := newTestSys(t, 0, true)
	staging := stageFile(t, ts, "mnt/copilot-init", "config.json", 0o644, `{}`)
	stageFile(t, ts, "mnt/copilot-init", "settings.json", 0o644, `{}`)
	stageFile(t, ts, "mnt/copilot-init", "permissions-config.json", 0o644, `{}`)
	ctx, _ := newTestContext(ts, Config{CopilotInit: staging, Home: "/root"})
	if err := copilotStagingPhase(ctx); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"config.json", "settings.json", "permissions-config.json"} {
		if !exists(ts, "/home/moatuser/.copilot/"+f) {
			t.Errorf("%s not staged", f)
		}
	}
}

func TestAgentStagingEmptyHomeStaysRootAnchored(t *testing.T) {
	// The script builds "$TARGET_HOME/.codex" by concatenation, so an empty
	// HOME yields the root-anchored "/.codex" — never a cwd-relative path.
	// Under the injected root that absolute path is creatable, proving the
	// destination stayed anchored.
	ts := newTestSys(t, 1000, false)
	staging := stageFile(t, ts, "mnt/codex-init", "config.toml", 0o644, "cfg")
	ctx, _ := newTestContext(ts, Config{CodexInit: staging, Home: ""})
	if err := codexStagingPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if !exists(ts, "/.codex/config.toml") {
		t.Error("empty HOME did not stage to the root-anchored /.codex")
	}
}

func TestAgentBlockIndependence(t *testing.T) {
	// AGENT-BLOCK-INDEPENDENCE-ORDER: with only Codex set, .claude and
	// .gemini are absent; blocks are guarded solely by their own var.
	ts := newTestSys(t, 0, true)
	staging := stageFile(t, ts, "mnt/codex-init", "config.toml", 0o644, "x")
	cfg := Config{CodexInit: staging, Home: "/root"}
	ctx, _ := newTestContext(ts, cfg)
	for _, phase := range []func(*Context) error{claudeStagingPhase, codexStagingPhase, geminiStagingPhase, copilotStagingPhase} {
		if err := phase(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if !exists(ts, "/home/moatuser/.codex/config.toml") {
		t.Error(".codex not populated")
	}
	for _, p := range []string{"/home/moatuser/.claude", "/home/moatuser/.gemini", "/home/moatuser/.copilot"} {
		if exists(ts, p) {
			t.Errorf("%s created without its init var", p)
		}
	}
}

func TestAgentChownFailureIsBestEffort(t *testing.T) {
	// AGENT-SET-E-COMPOUND-SEMANTICS: a chown failure is swallowed (the
	// 2>/dev/null || true idiom), the phase still succeeds.
	ts := newTestSys(t, 0, true)
	ts.chownErr = os.ErrPermission
	staging := stageFile(t, ts, "mnt/codex-init", "config.toml", 0o644, "x")
	ctx, _ := newTestContext(ts, Config{CodexInit: staging, Home: "/root"})
	if err := codexStagingPhase(ctx); err != nil {
		t.Fatalf("chown failure aborted the phase: %v", err)
	}
}

func TestAgentCopyFailureIsFatal(t *testing.T) {
	// Companion to best-effort chown: an unguarded cp failure aborts
	// (set -e). Make the destination unwritable by pre-creating the agent
	// dir as a read-only directory (non-root path so we lack override).
	ts := newTestSys(t, 1000, false)
	staging := stageFile(t, ts, "mnt/codex-init", "config.toml", 0o644, "x")
	roDir := filepath.Join(ts.Root, "tmp/h/.codex")
	if err := os.MkdirAll(roDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(roDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o755) })

	ctx, stderr := newTestContext(ts, Config{CodexInit: staging, Home: "/tmp/h"})
	err := codexStagingPhase(ctx)
	exit, ok := err.(exitError)
	if !ok || exit.code != 1 {
		t.Fatalf("err = %v, want exitError{1}", err)
	}
	if stderr.Len() == 0 {
		t.Error("fatal copy failure produced no stderr")
	}
}

func TestCodexStagingOpenAIEnvFile(t *testing.T) {
	// Companion to the anthropic-env.sh copy: the OpenAI BASH_ENV file must
	// land at $HOME/.codex/openai-env.sh or BASH_ENV points at nothing.
	ts := newTestSys(t, 0, true)
	staging := stageFile(t, ts, "mnt/codex-init", "config.toml", 0o644, "cfg")
	stageFile(t, ts, "mnt/codex-init", "openai-env.sh", 0o600, "export OPENAI_API_KEY=x")
	ctx, _ := newTestContext(ts, Config{CodexInit: staging, Home: "/root"})
	if err := codexStagingPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if got := statMode(t, ts, "/home/moatuser/.codex/openai-env.sh"); got != 0o600 {
		t.Errorf("openai-env.sh mode = %o, want 600 preserved", got)
	}
}

func TestCodexSubscriptionVersionGate(t *testing.T) {
	cases := []struct {
		name    string
		version string
		missing bool
		wantErr bool
	}{
		{name: "supported floor", version: "codex-cli 0.146.0"},
		{name: "supported ceiling", version: "codex-cli 0.154.0"},
		{name: "unsupported high", version: "codex-cli 0.155.0", wantErr: true},
		{name: "unsupported low", version: "codex-cli 0.145.9", wantErr: true},
		{name: "unknown output", version: "", wantErr: true},
		{name: "binary missing", missing: true, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := newTestSys(t, 0, true)
			staging := stageFile(t, ts, "mnt/codex-init", "config.toml", 0o644, "cfg")
			if tc.missing {
				ts.missingBinaries["codex"] = true
			}
			ts.runHook = func(c Cmd) (int, error) {
				if len(c.Argv) >= 2 && c.Argv[0] == "codex" && c.Argv[1] == "--version" {
					if w, ok := c.Stdout.(interface{ Write([]byte) (int, error) }); ok && tc.version != "" {
						_, _ = w.Write([]byte(tc.version + "\n"))
					}
				}
				return 0, nil
			}
			ctx, stderr := newTestContext(ts, Config{
				CodexInit:             staging,
				CodexSubscriptionAuth: "1",
				Home:                  "/root",
			})
			err := codexStagingPhase(ctx)
			if tc.wantErr {
				if exit, ok := err.(exitError); !ok || exit.code != 1 {
					t.Fatalf("err = %v, want exitError{1}", err)
				}
				if stderr.Len() == 0 {
					t.Error("fatal version gate produced no stderr")
				}
				return
			}
			if err != nil {
				t.Fatalf("version %q rejected: %v", tc.version, err)
			}
			if !exists(ts, "/home/moatuser/.codex/config.toml") {
				t.Error("supported version did not stage config.toml")
			}
		})
	}
}

func TestCodexSubscriptionGateOnlyForSubscription(t *testing.T) {
	// Companion: without MOAT_CODEX_SUBSCRIPTION_AUTH=1 an unsupported codex
	// must not block staging (API-key/MCP-only modes stage version-agnostic
	// files).
	ts := newTestSys(t, 0, true)
	staging := stageFile(t, ts, "mnt/codex-init", "auth.json", 0o644, `{"k":"v"}`)
	ts.runHook = func(Cmd) (int, error) { return 0, nil } // would report empty version
	ctx, _ := newTestContext(ts, Config{CodexInit: staging, Home: "/root"})
	if err := codexStagingPhase(ctx); err != nil {
		t.Fatalf("non-subscription staging gated on version: %v", err)
	}
	if !exists(ts, "/home/moatuser/.codex/auth.json") {
		t.Error("auth.json not staged without subscription auth")
	}
}

func TestCodexChownPrunesSessions(t *testing.T) {
	// The ~/.codex/sessions bind mount must not be re-owned: neither the
	// directory itself nor anything beneath it.
	ts := newTestSys(t, 0, true)
	staging := stageFile(t, ts, "mnt/codex-init", "config.toml", 0o644, "cfg")
	// A pre-existing sessions tree inside the target home (the bind mount).
	sessions := filepath.Join(ts.Root, "home/moatuser/.codex/sessions/2026/01/01")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessions, "rollout.jsonl"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, _ := newTestContext(ts, Config{CodexInit: staging, Home: "/root"})
	if err := codexStagingPhase(ctx); err != nil {
		t.Fatal(err)
	}
	if ts.chowned("/home/moatuser/.codex/sessions") {
		t.Error("sessions directory itself was chowned")
	}
	if ts.chowned("/home/moatuser/.codex/sessions/2026/01/01/rollout.jsonl") {
		t.Error("file under sessions was chowned")
	}
	if !ts.chowned("/home/moatuser/.codex/config.toml") {
		t.Error("config.toml was not chowned (prune over-applied)")
	}
}
