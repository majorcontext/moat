//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/deps"
	"github.com/majorcontext/moat/internal/run"
)

// TestCodexContainerConfig_E2E verifies that the Codex CLI in a real container
// accepts the configuration moat generates for it.
//
// The unit tests assert what moat writes; this asserts that the installed Codex
// binary agrees — it parses ~/.codex/config.toml and registers both MCP server
// kinds from it. That pairing is what drifted before: moat wrote a `.mcp.json`
// Codex never read, and passed a `--full-auto` flag Codex had deprecated.
//
// No OpenAI credential is exercised: `codex mcp list` reads config and exits
// without contacting the API, so the run needs no real key.
func TestCodexContainerConfig_E2E(t *testing.T) {
	// Use an isolated test keyring so the fake credential below never touches
	// the user's real credential store.
	t.Setenv("MOAT_KEYRING_SERVICE", "moat-test")
	t.Cleanup(func() { cleanupKeychainKey(t) })

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// A stored "openai" credential is what makes moat stage the Codex config.
	encKey, err := credential.DefaultEncryptionKey()
	if err != nil {
		t.Fatalf("DefaultEncryptionKey: %v", err)
	}
	credStore, err := credential.NewFileStore(credential.DefaultStoreDir(), encKey)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := credStore.Save(credential.Credential{
		Provider:  credential.ProviderOpenAI,
		Token:     "sk-e2e-not-a-real-key",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Save credential: %v", err)
	}
	defer credStore.Delete(credential.ProviderOpenAI)

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	cfg := &config.Config{
		Dependencies: []string{"node@22", "codex-cli"},
		Grants:       []string{"openai"},
		// Remote MCP server: becomes a streamable HTTP entry pointing at the
		// proxy relay. The URL is never dialed by `codex mcp list`.
		MCP: []config.MCPServerConfig{{
			Name: "e2e-remote",
			URL:  "https://mcp.example.com/mcp",
			Auth: &config.MCPAuthConfig{Grant: "openai", Header: "Authorization"},
		}},
	}
	// Sandbox-local MCP server: becomes a stdio entry.
	cfg.Codex.MCP = map[string]config.MCPServerSpec{
		"e2e-local": {Command: "echo", Args: []string{"hi"}},
	}

	// Markers keep the assertions anchored even if Codex adds banner output.
	script := strings.Join([]string{
		"echo MOAT_E2E_VERSION_START",
		"codex --version",
		"echo MOAT_E2E_CONFIG_START",
		`cat "$HOME/.codex/config.toml"`,
		"echo MOAT_E2E_MCP_START",
		// --json so the assertions below key off Codex's own output rather than
		// the config.toml dumped above, which contains the same server names.
		"codex mcp list --json || echo MOAT_E2E_MCP_FAILED",
		"echo MOAT_E2E_WORKSPACE_START",
		"ls -a /workspace",
	}, "; ")

	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-codex-config",
		Workspace: createTestWorkspace(t),
		Grants:    []string{"openai"},
		Config:    cfg,
		Cmd:       []string{"sh", "-c", script},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	if err := mgr.Start(ctx, r.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := mgr.Wait(ctx, r.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	logs, err := r.Store.ReadLogs(0, 2000)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}
	lines := make([]string, 0, len(logs))
	for _, entry := range logs {
		lines = append(lines, entry.Line)
	}
	out := strings.Join(lines, "\n")
	if !strings.Contains(out, "MOAT_E2E_CONFIG_START") {
		t.Fatalf("container produced no recognizable output:\n%s", out)
	}

	// The registry pin is what the container actually installed.
	spec, ok := deps.GetSpec("codex-cli")
	if !ok || spec.Default == "" {
		t.Fatal("codex-cli should be pinned in the registry")
	}
	if !strings.Contains(out, spec.Default) {
		t.Errorf("expected `codex --version` to report the pinned %s, output:\n%s", spec.Default, out)
	}

	// The generated config, as it lands in the container.
	for _, want := range []string{
		"approval_policy = 'never'",
		"sandbox_mode = 'danger-full-access'",
		"inherit = 'all'",
		"trust_level = 'trusted'",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("~/.codex/config.toml missing %q, output:\n%s", want, out)
		}
	}

	// Codex's own view of that config: both MCP servers registered, from
	// config.toml alone.
	if strings.Contains(out, "MOAT_E2E_MCP_FAILED") {
		t.Errorf("`codex mcp list` failed — Codex rejected the generated config, output:\n%s", out)
	}
	mcpJSON := out[strings.Index(out, "MOAT_E2E_MCP_START"):]
	if !strings.Contains(mcpJSON, `"e2e-local"`) {
		t.Errorf("`codex mcp list` did not report the sandbox-local server, output:\n%s", out)
	}
	if !strings.Contains(mcpJSON, `"e2e-remote"`) {
		t.Errorf("`codex mcp list` did not report the remote relay server, output:\n%s", out)
	}
	// Each kind must register as its own transport, not collapse into one.
	if !strings.Contains(mcpJSON, `"streamable_http"`) {
		t.Errorf("remote MCP server did not register as a streamable HTTP transport, output:\n%s", out)
	}
	if !strings.Contains(mcpJSON, `"stdio"`) {
		t.Errorf("sandbox-local MCP server did not register as a stdio transport, output:\n%s", out)
	}
	// The relay URL, not the server's real URL: the proxy injects the credential.
	if strings.Contains(out, "mcp.example.com") {
		t.Errorf("remote MCP server should point at the proxy relay, not its real URL, output:\n%s", out)
	}

	// Codex ignores .mcp.json, so moat must not leave one in the workspace.
	if strings.Contains(out, ".mcp.json") {
		t.Errorf("no .mcp.json should be written for a Codex run, output:\n%s", out)
	}
}

// TestCodexSessionSync_E2E verifies that codex.sync_logs actually mounts a
// session directory — the setting previously defaulted on and did nothing.
//
// It also pins the isolation boundary: by default the mount source is moat's
// own per-workspace directory, NOT the host's ~/.codex/sessions, so a container
// cannot read this host user's Codex transcripts from other projects.
func TestCodexSessionSync_E2E(t *testing.T) {
	t.Setenv("MOAT_KEYRING_SERVICE", "moat-test")
	t.Cleanup(func() { cleanupKeychainKey(t) })

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// Point HOME and MOAT_HOME at temp dirs so neither the host's real
	// ~/.codex nor ~/.moat is touched.
	hostHome := t.TempDir()
	t.Setenv("HOME", hostHome)
	moatHome := filepath.Join(hostHome, ".moat")
	t.Setenv("MOAT_HOME", moatHome)

	// A transcript from "another project" in the host's shared directory. The
	// container must not be able to see it under the default.
	hostSharedSessions := filepath.Join(hostHome, ".codex", "sessions", "2026", "01", "01")
	if err := os.MkdirAll(hostSharedSessions, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	otherProject := filepath.Join(hostSharedSessions, "rollout-other-project.jsonl")
	if err := os.WriteFile(otherProject, []byte(`{"secret":"other-project-phi"}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	encKey, err := credential.DefaultEncryptionKey()
	if err != nil {
		t.Fatalf("DefaultEncryptionKey: %v", err)
	}
	credStore, err := credential.NewFileStore(credential.DefaultStoreDir(), encKey)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := credStore.Save(credential.Credential{
		Provider:  credential.ProviderOpenAI,
		Token:     "sk-e2e-not-a-real-key",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Save credential: %v", err)
	}
	defer credStore.Delete(credential.ProviderOpenAI)

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	// Write a rollout-shaped file where Codex would, list what else is visible
	// in ~/.codex, and dump every transcript the container can reach.
	script := strings.Join([]string{
		"mkdir -p ~/.codex/sessions/2026/08/03",
		`echo '{"type":"session_meta"}' > ~/.codex/sessions/2026/08/03/rollout-e2e.jsonl`,
		"echo MOAT_E2E_CODEX_HOME_START",
		"ls -a ~/.codex",
		"echo MOAT_E2E_VISIBLE_SESSIONS_START",
		"cat ~/.codex/sessions/*/*/*/*.jsonl",
	}, "; ")

	workspace := createTestWorkspace(t)
	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-codex-session-sync",
		Workspace: workspace,
		Grants:    []string{"openai"},
		Config: &config.Config{
			Dependencies: []string{"node@22", "codex-cli"},
			Grants:       []string{"openai"},
		},
		Cmd: []string{"sh", "-c", script},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	if err := mgr.Start(ctx, r.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := mgr.Wait(ctx, r.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// The transcript must have landed on the host, under moat's per-workspace
	// directory rather than the host's shared Codex history.
	// Globbed rather than recomputed: the workspace-to-directory mapping is an
	// internal detail of the run package, and a test that reimplements it would
	// pass even if that mapping broke. Not fatal either — the isolation
	// assertions below are independent and must not be masked by a failure here.
	pattern := filepath.Join(moatHome, "codex", "sessions", "*", "2026", "08", "03", "rollout-e2e.jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("Glob(%q): %v", pattern, err)
	}
	switch {
	case len(matches) == 0:
		t.Errorf("session transcript did not sync to the host; no match for %s", pattern)
	case len(matches) > 1:
		t.Errorf("expected one synced transcript, got %v", matches)
	default:
		data, readErr := os.ReadFile(matches[0])
		if readErr != nil {
			t.Errorf("reading synced transcript %s: %v", matches[0], readErr)
		} else if !strings.Contains(string(data), "session_meta") {
			t.Errorf("host transcript has unexpected content: %s", data)
		}
	}

	// It must NOT have gone into the host's own Codex history.
	strayInShared := filepath.Join(hostHome, ".codex", "sessions", "2026", "08", "03", "rollout-e2e.jsonl")
	if _, err := os.Stat(strayInShared); err == nil {
		t.Errorf("transcript leaked into the host's shared ~/.codex/sessions at %s", strayInShared)
	}

	// The staged auth file must exist in the container but must NOT be on the
	// host: only the sessions subdirectory is shared, never ~/.codex itself.
	logs, err := r.Store.ReadLogs(0, 500)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}
	lines := make([]string, 0, len(logs))
	for _, entry := range logs {
		lines = append(lines, entry.Line)
	}
	out := strings.Join(lines, "\n")
	if !strings.Contains(out, "auth.json") {
		t.Errorf("expected the container's ~/.codex to hold auth.json, output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(hostHome, ".codex", "auth.json")); err == nil {
		t.Error("auth.json leaked to the host — the mount must cover the sessions dir only")
	}

	// The core isolation property: another project's transcript, sitting in the
	// host's shared Codex history, must be invisible inside the container.
	if strings.Contains(out, "other-project-phi") {
		t.Errorf("container could read another project's Codex transcript, output:\n%s", out)
	}
}
