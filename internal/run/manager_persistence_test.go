package run

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/majorcontext/moat/internal/container"

	"github.com/majorcontext/moat/internal/storage"
)

func TestObserversLeaveCompletionToOwner(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	store, err := storage.NewRunStore(storage.DefaultBaseDir(), "run_aabbccdd")
	if err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		exit := make(chan struct{})
		logsStarted, flush := make(chan struct{}), make(chan struct{})
		var waits, removals, captures atomic.Int32
		rt := &flexibleRuntime{
			waitFn: func(context.Context, string) (int64, error) {
				waits.Add(1)
				<-exit
				return 17, nil
			},
			removeFn: func(context.Context, string) error {
				removals.Add(1)
				return nil
			},
			containerLogsAllFn: func(context.Context, string) ([]byte, error) {
				captures.Add(1)
				close(logsStarted)
				<-flush
				return []byte("last line\n"), nil
			},
		}
		shared := &inspectRuntime{rt, func(context.Context, string) (string, error) {
			select {
			case <-exit:
				return "exited", nil
			default:
				return "running", nil
			}
		}}
		owner := newEdgeCaseManager(t, shared)
		r := &Run{ID: "run_aabbccdd", ContainerID: "ctr", Store: store, State: StateCreated, exitCh: make(chan struct{})}
		r.owner = ownerLock(store.Dir())
		if ok, err := r.owner.TryLock(); err != nil || !ok {
			t.Fatal(ok, err)
		}
		owner.runs[r.ID] = r
		if err := owner.Start(t.Context(), r.ID); err != nil {
			t.Fatal(err)
		}
		load := func() *Manager {
			m := newEdgeCaseManager(t, shared)
			if err := m.loadPersistedRuns(t.Context()); err != nil {
				t.Fatal(err)
			}
			return m
		}
		short := load()
		if err := short.Close(); err != nil {
			t.Fatal(err)
		}
		observer := load()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := observer.Wait(ctx, r.ID); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled observer wait returned %v", err)
		}
		result := make(chan error, 1)
		go func() { result <- observer.Wait(t.Context(), r.ID) }()
		synctest.Wait()
		if got := waits.Load(); got != 1 {
			t.Errorf("live runtime waiters = %d, want one owner", got)
		}
		close(exit)
		<-logsStarted
		time.Sleep(time.Second)
		select {
		case got := <-result:
			t.Fatalf("observer completed during log capture: %v", got)
		default:
		}
		close(flush)
		if err := <-result; err == nil || err.Error() != "exit code 17" {
			t.Fatalf("observer received %v", err)
		}
		if observer.runs[r.ID].GetState() != StateFailed {
			t.Fatal("stale observer state")
		}
		owner.monitorWg.Wait()
		if captures.Load() != 1 || removals.Load() != 1 {
			t.Fatalf("log captures = %d, removals = %d; want one each", captures.Load(), removals.Load())
		}
	})
}

func TestWaitForExitedRunWithoutOwner(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	store, err := storage.NewRunStore(storage.DefaultBaseDir(), "run_12345678")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMetadata(storage.Metadata{State: string(StateRunning), ContainerID: "stale"}); err != nil {
		t.Fatal(err)
	}
	// Use a separate process so SIGKILL releases a real kernel-held lock.
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=^TestOwnerLockHelper$")
	cmd.Env = append(os.Environ(), "MOAT_TEST_OWNER_DIR="+store.Dir())
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if msg, err := bufio.NewReader(out).ReadString('\n'); err != nil || msg != "locked\n" {
		t.Fatalf("owner: %q %v", msg, err)
	}
	var removals, captures atomic.Int32
	rt := &flexibleRuntime{
		states:             map[string]string{"stale": "exited"},
		containerLogsAllFn: func(context.Context, string) ([]byte, error) { captures.Add(1); return []byte("last line"), nil },
		waitFn:             func(context.Context, string) (int64, error) { return 23, nil },
		removeFn: func(context.Context, string) error {
			removals.Add(1)
			return nil
		},
	}
	m := newEdgeCaseManager(t, rt)
	if err := m.loadPersistedRuns(t.Context()); err != nil {
		t.Fatal(err)
	}
	other := newEdgeCaseManager(t, rt)
	if err := other.loadPersistedRuns(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	results := make(chan error, 2)
	for _, mgr := range []*Manager{m, other} {
		go func() { results <- mgr.Wait(t.Context(), store.RunID()) }()
	}
	for range 2 {
		if err := <-results; err == nil || err.Error() != "exit code 23" {
			t.Fatalf("unexpected orphan result: %v", err)
		}
	}
	if removals.Load() != 1 || captures.Load() != 1 {
		t.Fatalf("captures %d removes %d", captures.Load(), removals.Load())
	}
	meta, err := store.LoadMetadata()
	if err != nil || meta.State != string(StateFailed) || meta.CleanupPending {
		t.Fatalf("bad recovered metadata: %+v %v", meta, err)
	}
}

func TestOwnerLockHelper(t *testing.T) {
	dir := os.Getenv("MOAT_TEST_OWNER_DIR")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	lock := ownerLock(dir)
	ok, err := lock.TryLock()
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	_, _ = io.WriteString(os.Stdout, "locked\n")
	time.Sleep(time.Minute)
}

type inspectRuntime struct {
	*flexibleRuntime
	inspect func(context.Context, string) (string, error)
}

func (rt *inspectRuntime) ContainerState(ctx context.Context, id string) (string, error) {
	return rt.inspect(ctx, id)
}

func TestObserverCancellationDuringInspect(t *testing.T) {
	store, err := storage.NewRunStore(t.TempDir(), "id")
	if err != nil {
		t.Fatal(err)
	}
	meta := storage.Metadata{State: "running", ContainerID: "container"}
	if err := store.SaveMetadata(meta); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	rt := &inspectRuntime{&flexibleRuntime{}, func(context.Context, string) (string, error) { cancel(); return "", errors.New("CLI terminated") }}
	m := newEdgeCaseManager(t, rt)
	m.registerPersistedRun(StateRunning, meta, store, "id", nil)
	if err := m.Wait(ctx, "id"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestObserverRetriesTransientInspect(t *testing.T) {
	store, err := storage.NewRunStore(t.TempDir(), "id")
	if err != nil {
		t.Fatal(err)
	}
	meta := storage.Metadata{State: "running", ContainerID: "container"}
	if err := store.SaveMetadata(meta); err != nil {
		t.Fatal(err)
	}
	lock := ownerLock(store.Dir())
	if ok, err := lock.TryLock(); err != nil || !ok {
		t.Fatal(ok, err)
	}
	_ = lock.Unlock()
	calls := 0
	rt := &inspectRuntime{&flexibleRuntime{runtimeType: container.RuntimeApple}, func(context.Context, string) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("XPC timeout")
		}
		return "stopped", nil
	}}
	m := newEdgeCaseManager(t, rt)
	m.registerPersistedRun(StateRunning, meta, store, "id", nil)
	if err := m.Wait(t.Context(), "id"); err == nil || calls != 2 {
		t.Fatalf("calls %d result %v", calls, err)
	}
}

func TestStopDuringStartup(t *testing.T) {
	starting, release, exit := make(chan struct{}), make(chan struct{}), make(chan struct{})
	rt := &flexibleRuntime{
		startFn: func(context.Context, string) error { close(starting); <-release; return nil },
		stopFn:  func(context.Context, string) error { close(exit); return nil },
		waitFn:  func(context.Context, string) (int64, error) { <-exit; return 143, nil },
	}
	store, err := storage.NewRunStore(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	m := newEdgeCaseManager(t, rt)
	r := &Run{ID: "test", ContainerID: "ctr", Store: store, State: StateCreated, exitCh: make(chan struct{})}
	m.runs[r.ID] = r
	startDone, stopDone := make(chan error, 1), make(chan error, 1)
	go func() { startDone <- m.Start(t.Context(), r.ID) }()
	<-starting
	if err := m.Start(t.Context(), r.ID); err == nil {
		t.Fatal("duplicate start succeeded")
	}
	go func() { stopDone <- m.Stop(t.Context(), r.ID) }()
	close(release)
	if err := <-startDone; err != nil {
		t.Fatal(err)
	}
	if err := <-stopDone; err != nil {
		t.Fatal(err)
	}
	if err := m.Wait(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	meta, err := store.LoadMetadata()
	if err != nil || meta.State != "stopped" || meta.Error != "" {
		t.Fatal(meta.State, meta.Error, err)
	}
}

func TestDestroyCannotInterruptOwner(t *testing.T) {
	store, err := storage.NewRunStore(t.TempDir(), "r")
	if err != nil {
		t.Fatal(err)
	}
	meta := storage.Metadata{State: "running", ContainerID: "ctr"}
	if err := store.SaveMetadata(meta); err != nil {
		t.Fatal(err)
	}
	lock := ownerLock(store.Dir())
	if held, err := lock.TryLock(); !held || err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	m := newEdgeCaseManager(t, &flexibleRuntime{})
	m.registerPersistedRun(StateStopped, meta, store, "r", nil)
	if err := m.Destroy(t.Context(), "r"); err == nil {
		t.Fatal("destroyed a live owner's run")
	}
	if _, err := store.LoadMetadata(); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverUnstartedRun(t *testing.T) {
	store, err := storage.NewRunStore(t.TempDir(), "r")
	if err != nil {
		t.Fatal(err)
	}
	meta := storage.Metadata{State: "created", ContainerID: "ctr"}
	if err := store.SaveMetadata(meta); err != nil {
		t.Fatal(err)
	}
	lock := ownerLock(store.Dir())
	if held, err := lock.TryLock(); !held || err != nil {
		t.Fatal(err)
	}
	_ = lock.Unlock()
	m := newEdgeCaseManager(t, &flexibleRuntime{states: map[string]string{"ctr": "created"}})
	m.registerPersistedRun(StateCreated, meta, store, "r", nil)
	if err := m.Wait(t.Context(), "r"); err == nil {
		t.Fatal("ownerless created run reported success")
	}
	if m.runs["r"].GetState() != StateFailed {
		t.Fatal("created run not recovered")
	}
}

func TestRecoverInterruptedStartup(t *testing.T) {
	store, err := storage.NewRunStore(t.TempDir(), "r")
	if err != nil {
		t.Fatal(err)
	}
	meta := storage.Metadata{State: string(StateStarting), ContainerID: "ctr"}
	if err := store.SaveMetadata(meta); err != nil {
		t.Fatal(err)
	}
	lock := ownerLock(store.Dir())
	if held, err := lock.TryLock(); !held || err != nil {
		t.Fatal(held, err)
	}
	defer func() { _ = lock.Unlock() }()
	var stops int
	m := newEdgeCaseManager(t, &flexibleRuntime{
		states: map[string]string{"ctr": "running"},
		stopFn: func(ctx context.Context, _ string) error {
			stops++
			if d, ok := ctx.Deadline(); !ok || time.Until(d) < 10*time.Second {
				t.Fatal("stop used the short inspect deadline")
			}
			if stops == 1 {
				return errors.New("temporary runtime error")
			}
			return nil
		},
	})
	m.registerPersistedRun(StateRunning, meta, store, "r", nil)
	r := m.runs["r"]
	if done, err := m.pollOrRecoverRun(t.Context(), r); done || err != nil || stops != 0 {
		t.Fatalf("live owner interrupted: done=%v err=%v stops=%d", done, err, stops)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if err := m.Wait(t.Context(), "r"); err == nil || err.Error() != "owner exited during container startup" || stops != 2 {
		t.Fatalf("orphan recovery: err=%v stops=%d", err, stops)
	}
}

func TestFinalizationPreservesStartupFailure(t *testing.T) {
	for _, state := range []State{StateRunning, StateStopping} {
		t.Run(string(state), func(t *testing.T) {
			store, err := storage.NewRunStore(t.TempDir(), "r")
			if err != nil {
				t.Fatal(err)
			}
			m := newEdgeCaseManager(t, &flexibleRuntime{})
			r := &Run{ID: "r", ContainerID: "ctr", State: state, Store: store, Error: "firewall setup failed", exitCh: make(chan struct{})}
			m.runs[r.ID] = r
			if err := m.finishRun(r, 0, nil); err != nil {
				t.Fatal(err)
			}
			meta, err := store.LoadMetadata()
			if err != nil || meta.State != string(StateFailed) || meta.Error != "firewall setup failed" {
				t.Fatalf("saved result = %+v, %v", meta, err)
			}
			if err := m.Wait(t.Context(), r.ID); err == nil || err.Error() != meta.Error {
				t.Fatalf("Wait = %v", err)
			}
		})
	}
}

// A legacy run can still have a live owner collecting logs after guest exit.
func TestLegacyWaitLeavesPersistedRunToOwner(t *testing.T) {
	store, err := storage.NewRunStore(t.TempDir(), "r")
	if err != nil {
		t.Fatal(err)
	}
	meta := storage.Metadata{State: string(StateStarting), ContainerID: "ctr"}
	if err := store.SaveMetadata(meta); err != nil {
		t.Fatal(err)
	}
	rt := &flexibleRuntime{
		states:   map[string]string{"ctr": "running"},
		stopFn:   func(context.Context, string) error { t.Fatal("observer stopped legacy guest"); return nil },
		removeFn: func(context.Context, string) error { t.Fatal("observer removed legacy guest"); return nil },
		waitFn:   func(context.Context, string) (int64, error) { return 17, nil },
	}
	m := newEdgeCaseManager(t, rt)
	m.registerPersistedRun(StateRunning, meta, store, "r", nil)
	if done, err := m.pollOrRecoverRun(t.Context(), m.runs["r"]); done || err != nil {
		t.Fatalf("running legacy guest: done=%v err=%v", done, err)
	}
	rt.states["ctr"] = "exited"
	if err := m.Wait(t.Context(), "r"); err == nil || err.Error() != "exit code 17" {
		t.Fatalf("Wait = %v", err)
	}
	got, err := store.LoadMetadata()
	if err != nil || got.State != meta.State || got.Error != "" {
		t.Fatalf("legacy record after wait = %+v, %v", got, err)
	}
	if _, err := os.Stat(ownerLock(store.Dir()).Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy owner lock stat = %v", err)
	}
}

func TestLegacyTerminalWait(t *testing.T) {
	store, err := storage.NewRunStore(t.TempDir(), "r")
	if err != nil {
		t.Fatal(err)
	}
	meta := storage.Metadata{State: "failed", ContainerID: "ctr"}
	if err := store.SaveMetadata(meta); err != nil {
		t.Fatal(err)
	}
	m := newEdgeCaseManager(t, &flexibleRuntime{})
	m.registerPersistedRun(StateRunning, meta, store, "r", nil)
	if err := m.Wait(t.Context(), "r"); err != nil {
		t.Fatal(err)
	}
	if m.runs["r"].GetState() != StateFailed {
		t.Fatal("stale snapshot")
	}
	if err := store.Remove(); err != nil {
		t.Fatal(err)
	}
	if err := m.Wait(t.Context(), "r"); !errors.Is(err, ErrRunNotFound) {
		t.Fatal(err)
	}
}

func TestExternalStopWaitsForStartup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store, err := storage.NewRunStore(t.TempDir(), "r")
		if err != nil {
			t.Fatal(err)
		}
		entered, ready, exit := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var stopped atomic.Bool
		rt := &flexibleRuntime{
			startFn: func(context.Context, string) error { close(entered); <-ready; return nil },
			waitFn:  func(context.Context, string) (int64, error) { <-exit; return 143, nil },
			stopFn:  func(context.Context, string) error { stopped.Store(true); close(exit); return nil },
		}
		owner, observer := newEdgeCaseManager(t, rt), newEdgeCaseManager(t, rt)
		r := &Run{ID: "r", ContainerID: "ctr", State: StateCreated, Store: store, exitCh: make(chan struct{}), owner: ownerLock(store.Dir())}
		if held, err := r.owner.TryLock(); err != nil || !held {
			t.Fatal(held, err)
		}
		defer r.releaseOwner()
		owner.runs[r.ID] = r
		started := make(chan error, 1)
		go func() { started <- owner.Start(t.Context(), r.ID) }()
		<-entered
		meta, err := store.LoadMetadata()
		if err != nil {
			t.Fatal(err)
		}
		observer.registerPersistedRun(StateCreated, meta, store, "r", nil)
		request := make(chan error, 1)
		go func() { request <- observer.Stop(t.Context(), r.ID) }()
		time.Sleep(200 * time.Millisecond)
		if stopped.Load() {
			t.Fatal("stop overtook runtime startup")
		}
		close(ready)
		if err := <-started; err != nil {
			t.Fatal(err)
		}
		if err := <-request; err != nil {
			t.Fatal(err)
		}
		if !stopped.Load() || observer.runs["r"].GetState() != StateStopped {
			t.Fatal("observer failed to stop run")
		}
	})
}

// Stop reports completion of the operation even when a natural guest failure
// wins the race. Wait must still return that guest failure to both callers.
func TestStopRacingNaturalFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store, err := storage.NewRunStore(t.TempDir(), "r")
		if err != nil {
			t.Fatal(err)
		}
		exited, release := make(chan struct{}), make(chan struct{})
		rt := &flexibleRuntime{
			states: map[string]string{"ctr": "exited"},
			waitFn: func(context.Context, string) (int64, error) { close(exited); <-release; return 17, nil },
		}
		owner, observer := newEdgeCaseManager(t, rt), newEdgeCaseManager(t, rt)
		r := &Run{ID: "r", ContainerID: "ctr", State: StateCreated, Store: store, exitCh: make(chan struct{}), owner: ownerLock(store.Dir())}
		if held, err := r.owner.TryLock(); err != nil || !held {
			t.Fatal(held, err)
		}
		defer r.releaseOwner()
		owner.runs[r.ID] = r
		if err := owner.Start(t.Context(), r.ID); err != nil {
			t.Fatal(err)
		}
		<-exited
		meta, err := store.LoadMetadata()
		if err != nil {
			t.Fatal(err)
		}
		observer.registerPersistedRun(StateStopped, meta, store, r.ID, nil)
		done := make(chan error, 2)
		for _, mgr := range []*Manager{owner, observer} {
			go func() { done <- mgr.Stop(t.Context(), r.ID) }()
		}
		synctest.Wait()
		close(release)
		for range 2 {
			if err := <-done; err != nil {
				t.Fatalf("Stop = %v", err)
			}
		}
		for _, mgr := range []*Manager{owner, observer} {
			if err := mgr.Wait(t.Context(), r.ID); err == nil || err.Error() != "exit code 17" {
				t.Fatalf("Wait = %v", err)
			}
		}
	})
}
