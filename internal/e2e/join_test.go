//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	intcli "github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/daemon"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/storage"
)

// TestJoinHeadless starts a run that stays up, issues a headless join with
// -p, and asserts the three properties mandated by the join design:
//
//	(a) The join runs inside the SAME container (no new container created).
//	(b) No new proxy run registration is created for the join.
//	(c) The original run still owns teardown — stopping it terminates the
//	    container, which takes joins with it.
//
// Requirements this test CANNOT satisfy in this environment:
//   - A real container runtime (Docker or Apple containers) is required to
//     start a run and exec into it.  The sandbox here has neither, so the
//     test is skipped at runtime with an informative message.
//   - The join path calls manager.ExecInteractive, which requires a running
//     container; the manager.Get / StateRunning guards surface correctly in
//     unit tests (internal/run/manager_join_test.go).
//
// Wire status: structure is fully wired to real harness helpers (mgr.Create,
// mgr.Start, exec of the moat binary, daemon.ListRuns, storage.ReadLogs).
// The t.Skip fires before container operations when no runtime is available.
// Remove the t.Skip (and keep requireDocker) once a runtime is present in the
// test environment.
func TestJoinHeadless(t *testing.T) {
	// Skip until a container runtime is available in this environment.
	// Remove this line and keep the requireDocker call below to enable the test.
	t.Skip("requires a container runtime with ExecInteractive support; run manually with Docker or Apple containers")

	requireDocker(t)

	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		// --- Set up a long-lived run that stays alive for the join ---
		mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		defer mgr.Close()

		workspace := createTestWorkspace(t)

		// "sleep 60" keeps the primary alive long enough for the join.
		primaryRun, err := mgr.Create(ctx, run.Options{
			Name:      "e2e-join-primary",
			Workspace: workspace,
			Cmd:       []string{"sleep", "60"},
		})
		if err != nil {
			t.Fatalf("Create primary run: %v", err)
		}
		defer mgr.Destroy(context.Background(), primaryRun.ID)
		defer mgr.Stop(context.Background(), primaryRun.ID)

		if err := mgr.Start(ctx, primaryRun.ID); err != nil {
			t.Fatalf("Start primary run: %v", err)
		}

		// Wait briefly for the container to be fully running before joining.
		time.Sleep(500 * time.Millisecond)

		primaryContainerID := primaryRun.ContainerID
		if primaryContainerID == "" {
			t.Fatal("primary run has no container ID after Start")
		}

		// --- Assertion (b): count registered proxy runs before the join ---
		// moat join does not call daemon.RegisterRun; the join shares the
		// original run's proxy token.  Record the count now to compare after.
		daemonDir := filepath.Join(config.GlobalConfigDir(), "proxy")
		lock, lockErr := daemon.ReadLockFile(daemonDir)
		var runsBefore int
		if lockErr == nil && lock != nil && lock.IsAlive() {
			daemonClient := daemon.NewClient(lock.SockPath)
			listCtx, listCancel := context.WithTimeout(ctx, 3*time.Second)
			runs, listErr := daemonClient.ListRuns(listCtx)
			listCancel()
			if listErr == nil {
				runsBefore = len(runs)
			}
		}

		// --- Run the join (assertions a + b measured around it) ---
		// We invoke the real moat binary so the full CLI path is exercised
		// (provider resolution, ExecInteractive, log capture).
		moatBin := joinTestMoatExecutable(t)
		joinCmd := exec.CommandContext(ctx, moatBin,
			"join", primaryRun.ID, "claude",
			"-p", "echo HELLO_JOIN",
		)
		var joinOut bytes.Buffer
		joinCmd.Stdout = &joinOut
		joinCmd.Stderr = &joinOut

		joinErr := joinCmd.Run()
		// A non-zero exit is expected when claude is not installed in the
		// container; what matters is that no new container was created and
		// no new proxy registration appeared.
		t.Logf("join output: %s", joinOut.String())
		if joinErr != nil {
			t.Logf("join exited with error (may be expected if claude not installed): %v", joinErr)
		}

		// Assertion (a): no new container — the primary container ID is unchanged.
		refreshed, getErr := mgr.Get(primaryRun.ID)
		if getErr != nil {
			t.Fatalf("Get primary run after join: %v", getErr)
		}
		if refreshed.ContainerID != primaryContainerID {
			t.Errorf("container ID changed after join: before=%q after=%q (join must reuse the existing container)",
				primaryContainerID, refreshed.ContainerID)
		}

		// Assertion (b): proxy registration count unchanged.
		if lockErr == nil && lock != nil && lock.IsAlive() {
			daemonClient := daemon.NewClient(lock.SockPath)
			listCtx, listCancel := context.WithTimeout(ctx, 3*time.Second)
			runsAfter, listErr := daemonClient.ListRuns(listCtx)
			listCancel()
			if listErr == nil && len(runsAfter) != runsBefore {
				t.Errorf("proxy registration count changed from %d to %d after join — join must not register a new run",
					runsBefore, len(runsAfter))
			}
		}

		// --- Assertion (c): original run owns teardown ---
		// Stopping the primary tears down the container.  Verify the run
		// transitions to a terminal state.
		if stopErr := mgr.Stop(ctx, primaryRun.ID); stopErr != nil {
			t.Fatalf("Stop primary run: %v", stopErr)
		}

		// Poll briefly for the stopped state (teardown may be async).
		deadline := time.Now().Add(10 * time.Second)
		var finalState run.State
		for time.Now().Before(deadline) {
			r, err := mgr.Get(primaryRun.ID)
			if err != nil {
				break
			}
			finalState = r.GetState()
			if finalState == run.StateStopped || finalState == run.StateFailed {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if finalState != run.StateStopped && finalState != run.StateFailed {
			t.Errorf("primary run state after stop = %q, want %q or %q",
				finalState, run.StateStopped, run.StateFailed)
		}

		// Verify the join's log output landed in the indexed join log file
		// (logs.1.jsonl) rather than the primary's logs.jsonl, demonstrating
		// split-console capture.
		time.Sleep(100 * time.Millisecond)
		store, storeErr := storage.NewRunStore(storage.DefaultBaseDir(), primaryRun.ID)
		if storeErr != nil {
			t.Logf("NewRunStore: %v (non-fatal)", storeErr)
		} else {
			primaryLogs, _ := store.ReadLogs(0, 100)
			for _, entry := range primaryLogs {
				if strings.Contains(entry.Line, "HELLO_JOIN") {
					t.Errorf("join output appeared in primary logs.jsonl; expected logs.1.jsonl for split-console isolation")
					break
				}
			}
		}
	})
}

// TestDualAgentJoin_E2E provisions a real container via moat.yaml's
// `agents: [claude, codex]` and proves it is joinable as BOTH agents — the
// combination TestJoinHeadless doesn't cover (it is single-agent).
//
// What it asserts:
//  1. The persisted joinable_agents set (what moat actually provisioned —
//     internal/run/joinable.go's computeJoinableAgents) contains both
//     "claude" and "codex", proving `agents:` expansion (dependencies +
//     grants) actually ran and actually staged both agents.
//  2. `moat join <run> claude` and `moat join <run> codex` both get PAST
//     validateJoinAgent's capability gate: neither is refused with the
//     "cannot host" error that fires for an agent the run never provisioned
//     (cmd/moat/cli/join_cmd.go). That refusal path is already covered by
//     pure unit tests (join_cmd_test.go); what only a real container proves
//     is the positive case — that both real, distinct in-container agent
//     binaries are reachable through the same gate on the same run.
//  3. Both joins run inside the SAME container the primary run started (no
//     new container created), matching TestJoinHeadless's invariant (a), and
//     joined output does not leak into the primary's logs.jsonl (split
//     console isolation).
//
// What this test CANNOT assert: that claude/codex complete a real
// conversation turn. This sandbox has no real Anthropic or OpenAI
// credentials, so both grants are stored with fake tokens; the real CLI
// processes are expected to fail authentication once they reach the network.
// That failure is fine and out of scope — what matters here is purely the
// join *mechanism*, not agent behavior once inside.
func TestDualAgentJoin_E2E(t *testing.T) {
	// Isolated test keyring so the fake credentials below never touch the
	// user's real credential store (same pattern as TestCodexContainerConfig_E2E).
	t.Setenv("MOAT_KEYRING_SERVICE", "moat-test")
	t.Cleanup(func() { cleanupKeychainKey(t) })

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// Fake credentials for both grants `agents: [claude, codex]` expands to.
	// Neither needs to look like a real token: the claude provider writes a
	// fixed placeholder into the container regardless of the stored value
	// (internal/providers/claude/config.go WriteCredentialsFile — the real
	// token is never written to the container), and codex's staged auth.json
	// is likewise a placeholder the proxy replaces at request time. Storing
	// *something* under each provider is what satisfies validateGrants and
	// gets each agent into JoinableAgents.
	encKey, err := credential.DefaultEncryptionKey()
	if err != nil {
		t.Fatalf("DefaultEncryptionKey: %v", err)
	}
	credStore, err := credential.NewFileStore(credential.DefaultStoreDir(), encKey)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := credStore.Save(credential.Credential{
		Provider:  credential.ProviderClaude,
		Token:     "e2e-not-a-real-oauth-token",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Save claude credential: %v", err)
	}
	defer credStore.Delete(credential.ProviderClaude)
	if err := credStore.Save(credential.Credential{
		Provider:  credential.ProviderOpenAI,
		Token:     "sk-e2e-not-a-real-key",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Save openai credential: %v", err)
	}
	defer credStore.Delete(credential.ProviderOpenAI)

	// moat.yaml's `agents:` list is the thing under test. ExpandAgents is the
	// exact function `moat run` calls (cmd/moat/cli/run.go) to turn it into
	// dependencies, grants, and network rules — using it here, rather than
	// hand-building a Config with Dependencies/Grants set directly, is what
	// makes this test exercise the `agents:` feature and not just the join
	// gate in isolation.
	workspace := t.TempDir()
	yaml := "agents: [claude, codex]\nversion: 1.0.0\n"
	if err := os.WriteFile(filepath.Join(workspace, "moat.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("WriteFile moat.yaml: %v", err)
	}
	cfg, err := config.Load(workspace)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	// ExpandAgents returns derived grants rather than merging them into
	// cfg.Grants (see its doc comment) — merge them the same way
	// cmd/moat/cli/run.go does before passing grants to the run manager.
	derivedGrants, err := intcli.ExpandAgents(cfg)
	if err != nil {
		t.Fatalf("ExpandAgents: %v", err)
	}
	grants := append(append([]string{}, cfg.Grants...), derivedGrants...)

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	// "sleep 600" keeps the container alive long enough for both joins, and —
	// crucially for the split-console assertion below — never writes to
	// stdout itself, so anything found in logs.jsonl can only have leaked
	// from a join.
	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-dual-agent-join",
		Workspace: workspace,
		Grants:    grants,
		Config:    cfg,
		Cmd:       []string{"sleep", "600"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)
	defer mgr.Stop(context.Background(), r.ID)

	if err := mgr.Start(ctx, r.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait briefly for the container to be fully running before joining.
	time.Sleep(500 * time.Millisecond)

	primaryContainerID := r.ContainerID
	if primaryContainerID == "" {
		t.Fatal("run has no container ID after Start")
	}

	// --- Assertion (1): both agents landed in the persisted capability set ---
	refreshed, err := mgr.Get(r.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	t.Logf("JoinableAgents = %v", refreshed.JoinableAgents)
	gotAgents := make(map[string]bool, len(refreshed.JoinableAgents))
	for _, a := range refreshed.JoinableAgents {
		gotAgents[a] = true
	}
	for _, want := range []string{"claude", "codex"} {
		if !gotAgents[want] {
			t.Fatalf("JoinableAgents = %v, missing %q — `agents: [claude, codex]` did not provision it as claimed",
				refreshed.JoinableAgents, want)
		}
	}

	moatBin := joinTestMoatExecutable(t)

	// --- Assertion (2): join as claude, then as codex ---
	// Real moat binary, so the full CLI path is exercised (agent/provider
	// resolution, validateJoinAgent, ExecInteractive) — same approach as
	// TestJoinHeadless. Headless (-p) avoids needing a pty.
	//
	// Two checks per join, and both are load-bearing:
	//   - Negative: the output must not contain "cannot host" — the literal
	//     string validateJoinAgent (join_cmd.go) emits when the gate refuses
	//     an agent that isn't in JoinableAgents.
	//   - Positive: the output must be non-empty. Without this, the negative
	//     check alone passes vacuously if the subprocess never launches, or
	//     the joined process crashes before printing anything — out stays
	//     "", and "" plainly does not contain "cannot host" either. Fatal,
	//     not merely logged: in 3/3 real runs this captured genuine evidence
	//     the join reached a live process (claude's real 401 from Anthropic;
	//     codex's own trust-directory check), so it carries no flake risk
	//     here — see runJoinHeadlessCLI's doc comment for what "output"
	//     means and why it's safe to require.
	claudeOut := runJoinHeadlessCLI(t, moatBin, r.ID, "claude", "say OK and nothing else")
	if strings.Contains(claudeOut, "cannot host") {
		t.Errorf("join claude was refused by the capability gate despite being in JoinableAgents:\n%s", claudeOut)
	}
	if strings.TrimSpace(claudeOut) == "" {
		t.Fatalf("join claude produced no output at all — cannot tell whether the gate passed or the subprocess never ran")
	}

	codexOut := runJoinHeadlessCLI(t, moatBin, r.ID, "codex", "say OK and nothing else")
	if strings.Contains(codexOut, "cannot host") {
		t.Errorf("join codex was refused by the capability gate despite being in JoinableAgents:\n%s", codexOut)
	}
	if strings.TrimSpace(codexOut) == "" {
		t.Fatalf("join codex produced no output at all — cannot tell whether the gate passed or the subprocess never ran")
	}

	// --- Assertion (3a): no new container across either join ---
	afterJoins, err := mgr.Get(r.ID)
	if err != nil {
		t.Fatalf("Get after joins: %v", err)
	}
	if afterJoins.ContainerID != primaryContainerID {
		t.Errorf("container ID changed after joins: before=%q after=%q (join must reuse the existing container)",
			primaryContainerID, afterJoins.ContainerID)
	}

	// --- Assertion (3b): split-console isolation ---
	// The primary command is "sleep 600"; it never writes to stdout, so
	// logs.jsonl must stay empty regardless of what either joined agent
	// printed.
	time.Sleep(100 * time.Millisecond)
	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	primaryLogs, logsErr := store.ReadLogs(0, 500)
	if logsErr != nil {
		t.Errorf("ReadLogs: %v", logsErr)
	} else if len(primaryLogs) != 0 {
		t.Errorf("primary logs.jsonl should be empty (\"sleep 600\" writes nothing), got %d lines — joined agent output leaked in: %+v",
			len(primaryLogs), primaryLogs)
	}

	// Deliberately NOT asserted: the on-disk logs.<N>.jsonl file. That would
	// be a stronger check in principle (proof the output was actually teed
	// to storage, not just visible to this test's own subprocess capture),
	// but runJoinHeadless (join_cmd.go) only tees the joined process's stdout
	// into logs.<N>.jsonl, not its stderr — and claude/codex may write an
	// auth failure to either stream. Asserting on logs.<N>.jsonl here would
	// couple this test to that pre-existing, out-of-scope asymmetry. The
	// fatal non-empty checks above already use the reliable signal: the
	// `moat join` subprocess's own combined stdout+stderr, captured directly
	// by runJoinHeadlessCLI regardless of which stream the joined agent used.
}

// runJoinHeadlessCLI runs `moat join <runID> <agent> -p <prompt>` via the
// real moat binary and returns its combined stdout+stderr — the `moat join`
// process's own streams, which is why it reliably captures the joined
// agent's output regardless of whether that agent wrote to its own stdout or
// stderr (see the comment above the call sites for why that distinction
// matters). The caller, not this helper, decides what the returned string
// must contain; this helper only runs the process and reports what happened.
//
// It deliberately does NOT fail the test on a non-zero exit: the joined
// agents in this test carry fake credentials, so a failed auth attempt is
// expected and is not what this test is checking — a real exit code from a
// real process that got past the gate is success, not failure, for this
// test's purposes. (What the caller DOES require is that some output was
// produced at all — see the fatal checks at the call sites — which is a
// different, and necessary, signal from the exit code.)
//
// Bounded to 90s so a CLI that unexpectedly blocks on interactive auth
// (rather than failing fast) doesn't hang the suite — moat join runs
// headless (no TTY), which should prevent that, but the bound makes the
// failure mode "test times out with a clear log" rather than "test hangs
// forever."
func runJoinHeadlessCLI(t *testing.T, moatBin, runID, agent, prompt string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, moatBin, "join", runID, agent, "-p", prompt)
	// join_cmd.go calls run.NewManager() unconditionally (it has no
	// --no-sandbox flag of its own — joining execs into an already-running
	// container, so it shouldn't need one), but NewManager() still probes for
	// gVisor up front as part of building the runtime pool. This sandbox has
	// no gVisor (runsc) installed, so without this the join subprocess fails
	// before ever reaching validateJoinAgent — the same MOAT_NO_SANDBOX=1
	// escape hatch Task 17's manual verification needed for `moat run`.
	cmd.Env = append(os.Environ(), "MOAT_NO_SANDBOX=1")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()
	t.Logf("join %s output: %s", agent, out.String())
	if runErr != nil {
		t.Logf("join %s exited with error (may be expected — fake credentials): %v", agent, runErr)
	}
	return out.String()
}

// joinTestMoatExecutable returns the path to the moat binary set by TestMain
// via MOAT_EXECUTABLE, skipping the test if it is not set.
func joinTestMoatExecutable(t *testing.T) string {
	t.Helper()
	if exe := os.Getenv("MOAT_EXECUTABLE"); exe != "" {
		return exe
	}
	t.Skip("MOAT_EXECUTABLE not set; skip join CLI test")
	return ""
}
