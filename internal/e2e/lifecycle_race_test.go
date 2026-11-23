//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/storage"
)

// newLifecycleManager returns a manager that the test closes on cleanup.
func newLifecycleManager(t *testing.T) *run.Manager {
	t.Helper()
	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { mgr.Close() })
	return mgr
}

// startOwned starts a run in the given mode. The returned channel receives
// the owner's result for the run: StartAttached's return value for a
// foreground run, Wait's for a background one.
func startOwned(ctx context.Context, t *testing.T, mgr *run.Manager, r *run.Run, interactive bool) <-chan error {
	t.Helper()
	result := make(chan error, 1)
	if interactive {
		go func() {
			var out bytes.Buffer
			result <- mgr.StartAttached(ctx, r.ID, nil, &out, &out, container.TTYSize{})
		}()
		return result
	}
	if err := mgr.Start(ctx, r.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	go func() { result <- mgr.Wait(ctx, r.ID) }()
	return result
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s did not appear within %s", path, timeout)
}

func receive(t *testing.T, what string, ch <-chan error, timeout time.Duration) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(timeout):
		t.Fatalf("%s did not return within %s", what, timeout)
		return nil
	}
}

// TestStopRacingGuestExit stops a run from a second manager at about the
// moment the guest exits 17 on its own. Either side may win. Whichever does,
// Stop must return, the owner and the observer must report the same result,
// and the output must be stored exactly once.
func TestStopRacingGuestExit(t *testing.T) {
	const guestLifetime = 2 * time.Second
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		for _, interactive := range []bool{false, true} {
			// A negative lead stops just before the guest exits, a positive
			// one just after.
			for _, lead := range []time.Duration{-150 * time.Millisecond, 0, 150 * time.Millisecond} {
				t.Run(fmt.Sprintf("interactive=%v/lead=%s", interactive, lead), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
					defer cancel()
					owner := newLifecycleManager(t)
					workspace := createTestWorkspace(t)

					r, err := owner.Create(ctx, run.Options{
						Name:        "test-stop-race",
						Workspace:   workspace,
						Cmd:         []string{"sh", "-c", "echo ran; touch /workspace/started; sleep 2; exit 17"},
						Interactive: interactive,
					})
					if err != nil {
						t.Fatalf("Create: %v", err)
					}
					defer owner.Destroy(context.Background(), r.ID)

					ownerResult := startOwned(ctx, t, owner, r, interactive)
					waitForFile(t, filepath.Join(workspace, "started"), 2*time.Minute)
					started := time.Now()

					// The observer loads the run from disk, as a second CLI would.
					observer := newLifecycleManager(t)
					if _, err := observer.Get(r.ID); err != nil {
						t.Fatalf("observer does not see the run: %v", err)
					}
					time.Sleep(time.Until(started.Add(guestLifetime + lead)))

					stopped := make(chan error, 1)
					go func() { stopped <- observer.Stop(ctx, r.ID) }()
					if err := receive(t, "observer Stop", stopped, 90*time.Second); err != nil {
						t.Fatalf("observer Stop: %v", err)
					}

					ownerErr := receive(t, "owner", ownerResult, 90*time.Second)
					waited := make(chan error, 1)
					go func() { waited <- observer.Wait(ctx, r.ID) }()
					observerErr := receive(t, "observer Wait", waited, 90*time.Second)

					state := r.GetState()
					t.Logf("outcome: %s (owner = %v)", state, ownerErr)
					switch state {
					case run.StateStopped:
						if ownerErr != nil || observerErr != nil {
							t.Errorf("stopped: owner = %v, observer = %v; want nil from both", ownerErr, observerErr)
						}
					case run.StateFailed:
						for who, err := range map[string]error{"owner": ownerErr, "observer": observerErr} {
							if err == nil || !strings.Contains(err.Error(), "exit code 17") {
								t.Errorf("failed: %s = %v, want exit code 17", who, err)
							}
						}
					default:
						t.Fatalf("state = %s, want stopped or failed", state)
					}

					meta, err := r.Store.LoadMetadata()
					if err != nil {
						t.Fatalf("LoadMetadata: %v", err)
					}
					if meta.State != string(state) {
						t.Errorf("persisted state = %s, owner state = %s", meta.State, state)
					}
					if n := countLogLine(t, r.Store, "ran"); n != 1 {
						t.Errorf("log holds %d copies of the guest's line, want 1", n)
					}
				})
			}
		}
	})
}

func countLogLine(t *testing.T, store *storage.RunStore, line string) int {
	t.Helper()
	entries, err := store.ReadLogs(0, 1000)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}
	n := 0
	for _, e := range entries {
		if strings.TrimSpace(e.Line) == line {
			n++
		}
	}
	return n
}

// TestCanceledWaitLeavesRunRunning verifies that a waiter which gives up,
// in the owner or in an observer, neither ends nor finalizes the run, and
// that the run can still be stopped afterwards.
func TestCanceledWaitLeavesRunRunning(t *testing.T) {
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		owner := newLifecycleManager(t)

		r, err := owner.Create(ctx, run.Options{
			Name:      "test-canceled-wait",
			Workspace: createTestWorkspace(t),
			Cmd:       []string{"sleep", "300"},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		defer owner.Destroy(context.Background(), r.ID)
		if err := owner.Start(ctx, r.ID); err != nil {
			t.Fatalf("Start: %v", err)
		}
		observer := newLifecycleManager(t)

		for name, mgr := range map[string]*run.Manager{"owner": owner, "observer": observer} {
			waitCtx, cancelWait := context.WithTimeout(ctx, 2*time.Second)
			err := mgr.Wait(waitCtx, r.ID)
			cancelWait()
			if err != context.DeadlineExceeded {
				t.Errorf("%s Wait = %v, want deadline exceeded", name, err)
			}
		}
		if state := r.GetState(); state != run.StateRunning {
			t.Fatalf("state after canceled waits = %s, want running", state)
		}
		if state, err := rt.ContainerState(ctx, r.ContainerID); err != nil || state != "running" {
			t.Fatalf("container state = %q, %v; want running", state, err)
		}

		stopCtx, cancelStop := context.WithTimeout(ctx, 90*time.Second)
		defer cancelStop()
		if err := observer.Stop(stopCtx, r.ID); err != nil {
			t.Fatalf("Stop after canceled waits: %v", err)
		}
		if err := owner.Wait(stopCtx, r.ID); err != nil {
			t.Errorf("owner Wait after stop = %v, want nil", err)
		}
		if state := r.GetState(); state != run.StateStopped {
			t.Errorf("state = %s, want stopped", state)
		}
	})
}

// TestAppleLaunchLossKeepsRunStoppable is Apple-only. The adapter learns the
// guest's exit status from the `container start --attach` process it holds.
// If that process dies, the guest keeps running: the run must stay running,
// and a later stop must still complete.
func TestAppleLaunchLossKeepsRunStoppable(t *testing.T) {
	requireApple(t)
	t.Setenv("MOAT_RUNTIME", "apple")
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	mgr := newLifecycleManager(t)

	r, err := mgr.Create(ctx, run.Options{
		Name:      "test-launch-loss",
		Workspace: createTestWorkspace(t),
		Cmd:       []string{"sleep", "300"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)
	if err := mgr.Start(ctx, r.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if out, err := exec.Command("pkill", "-KILL", "-f", "container start --attach "+r.ContainerID).CombinedOutput(); err != nil {
		t.Fatalf("kill launch process: %v: %s", err, out)
	}

	waitCtx, cancelWait := context.WithTimeout(ctx, 5*time.Second)
	err = mgr.Wait(waitCtx, r.ID)
	cancelWait()
	if err != context.DeadlineExceeded {
		t.Fatalf("Wait after losing the launch process = %v, want the run to keep running", err)
	}
	if state := r.GetState(); state != run.StateRunning {
		t.Fatalf("state = %s, want running", state)
	}

	stopCtx, cancelStop := context.WithTimeout(ctx, 90*time.Second)
	defer cancelStop()
	if err := mgr.Stop(stopCtx, r.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if state := r.GetState(); state != run.StateStopped {
		t.Errorf("state = %s, want stopped", state)
	}
}

// TestStartFailureIsRecorded starts a command that does not exist in the
// image. Both runtimes must fail the start rather than report a run that
// exited, record the run as failed with the cause, and leave nothing running.
func TestStartFailureIsRecorded(t *testing.T) {
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		for _, interactive := range []bool{false, true} {
			t.Run(fmt.Sprintf("interactive=%v", interactive), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()
				mgr := newLifecycleManager(t)

				r, err := mgr.Create(ctx, run.Options{
					Name:        "test-start-failure",
					Workspace:   createTestWorkspace(t),
					Cmd:         []string{"/no/such/moat-test-binary"},
					Interactive: interactive,
				})
				if err != nil {
					t.Fatalf("Create: %v", err)
				}
				defer mgr.Destroy(context.Background(), r.ID)

				var startErr error
				if interactive {
					var out bytes.Buffer
					startErr = mgr.StartAttached(ctx, r.ID, nil, &out, &out, container.TTYSize{})
				} else {
					startErr = mgr.Start(ctx, r.ID)
				}
				if startErr == nil {
					t.Fatal("start succeeded for a command that does not exist")
				}
				if strings.Contains(startErr.Error(), "exit code") {
					t.Errorf("start failure reported as a guest exit: %v", startErr)
				}
				if state := r.GetState(); state != run.StateFailed {
					t.Errorf("state = %s, want failed", state)
				}
				meta, err := r.Store.LoadMetadata()
				if err != nil {
					t.Fatalf("LoadMetadata: %v", err)
				}
				if meta.State != string(run.StateFailed) || meta.Error == "" {
					t.Errorf("persisted state = %s, error = %q; want failed with a cause", meta.State, meta.Error)
				}
				if state, err := rt.ContainerState(ctx, r.ContainerID); err == nil && state == "running" {
					t.Errorf("container still running after a failed start")
				}

				// A failed start must not leave Wait or Stop hanging.
				bounded, cancelBounded := context.WithTimeout(ctx, 60*time.Second)
				defer cancelBounded()
				if err := mgr.Wait(bounded, r.ID); err == nil {
					t.Error("Wait = nil after a failed start, want the start error")
				} else if bounded.Err() != nil {
					t.Errorf("Wait hung after a failed start: %v", err)
				}
				if err := mgr.Stop(bounded, r.ID); err != nil {
					t.Errorf("Stop after a failed start: %v", err)
				}
			})
		}
	})
}
