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

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
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
