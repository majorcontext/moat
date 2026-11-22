package run

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/storage"
)

func containerExited(state string) bool {
	return state == "exited" || state == "stopped" || state == "dead"
}

func ownerLock(dir string) *flock.Flock {
	return flock.New(filepath.Join(dir, "owner.lock"), flock.SetPermissions(0o600))
}

// Only the creator has the launch configuration (including firewall settings
// and live SSH registration). A run ID names one execution.
func (m *Manager) beginStart(id string) (*Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrRunNotFound, id)
	}
	if r.inherited || r.GetState() != StateCreated {
		return nil, fmt.Errorf("cannot start run %s: requires a newly created local run", id)
	}
	if m.closing {
		return nil, errors.New("run manager is closing")
	}
	m.activeOps.Add(1)
	r.startupDone = make(chan struct{})
	r.SetState(StateStarting)
	if err := r.SaveMetadata(); err != nil {
		r.SetStateFailedAt(err.Error(), time.Now())
		r.notifyExit(err)
		r.releaseOwner()
		close(r.startupDone)
		m.activeOps.Done()
		return nil, err
	}
	return r, nil
}

// awaitAttachedStart handles a fast exit before readiness, including its
// attach result. A running process is reported only after a successful inspect.
func (m *Manager) awaitAttachedStart(ctx context.Context, r *Run, done <-chan error) (running bool, attachErr, errorStarting error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	t := time.NewTicker(30 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case err := <-done:
			return false, err, nil
		case <-ctx.Done():
			return false, nil, ctx.Err()
		case <-t.C:
			state, err := m.defaultRuntime().ContainerState(ctx, r.ContainerID)
			if err == nil && state == "running" {
				return true, nil, nil
			}
		}
	}
}

// A launch or transport failure can leave the guest alive. Confirm stop (or
// force removal) before declaring failure and releasing its resources.
func (m *Manager) abortStart(r *Run, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rt := m.defaultRuntime()
	if err := rt.StopContainer(ctx, r.ContainerID); err != nil {
		removeCtx, release := context.WithTimeout(context.Background(), 15*time.Second)
		defer release()
		if rmErr := rt.RemoveContainer(removeCtx, r.ContainerID); rmErr != nil {
			failure := errors.Join(cause, err, rmErr)
			r.SetStateWithError(StateRunning, failure.Error())
			saveErr := r.SaveMetadata()
			m.watchExit(r)
			return errors.Join(failure, saveErr)
		}
	}
	return errors.Join(cause, m.finishRun(r, 0, cause))
}

func (m *Manager) watchExit(r *Run) {
	m.monitorWg.Add(1)
	go func() { defer m.monitorWg.Done(); m.monitorContainerExit(m.monitorCtx, r) }()
}

// If a stop escape coincides with natural exit, the attachment has already
// given up its terminal result. Recover the guest status where available.
func (m *Manager) finalizeAbandonedAttach(ctx context.Context, r *Run) error {
	rt, err := m.runtimeForRun(r)
	if err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if rt.Type() == container.RuntimeApple {
		return m.finishRun(r, 0, errors.New("exit status unknown after interactive attachment ended"))
	}
	code, err := rt.WaitContainer(waitCtx, r.ContainerID)
	if err != nil {
		return err
	}
	return m.finishRun(r, code, nil)
}

func (r *Run) notifyExit(err error) {
	if r.exitCh == nil {
		return
	}
	r.signalExit.Do(func() {
		r.stateMu.Lock()
		r.completionErr = err
		r.stateMu.Unlock()
		close(r.exitCh)
	})
}

func (r *Run) releaseOwner() {
	if r.owner != nil {
		_ = r.owner.Unlock()
	}
}

// Serialize all local finalizers, including an explicit Stop racing the
// runtime waiter. Publish the result before removing the container.
func (m *Manager) finishRun(r *Run, code int64, exitErr error) (result error) {
	r.finishMu.Lock()
	defer r.finishMu.Unlock()
	if r.exitCh != nil {
		select {
		case <-r.exitCh:
			return nil
		default:
		}
	}
	defer func() { r.notifyExit(result); r.releaseOwner() }()
	m.captureLogs(r)
	runProviderStoppedHooks(r)
	r.stateMu.Lock()
	state, priorFailure := r.State, r.Error
	r.stateMu.Unlock()
	if state != StateStopped && state != StateFailed {
		stopped := state == StateStopping
		if r.Store != nil {
			lock := stopLock(r)
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			ok, err := lock.TryLockContext(ctx, 25*time.Millisecond)
			if err != nil {
				return err
			}
			if !ok {
				return ctx.Err()
			}
			_, err = os.Stat(filepath.Join(r.Store.Dir(), "stop.request"))
			_ = lock.Unlock()
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			stopped = stopped || err == nil
		}
		switch {
		case priorFailure != "":
			// Containment failed during startup. A later stop or clean guest
			// exit ends the process but cannot make its startup successful.
			r.SetStateFailedAt(priorFailure, time.Now())
		case stopped:
			r.SetStateWithTime(StateStopped, time.Now())
		case exitErr != nil:
			r.SetStateFailedAt(exitErr.Error(), time.Now())
		case code != 0:
			r.SetStateFailedAt(fmt.Sprintf("exit code %d", code), time.Now())
		default:
			r.SetStateWithTime(StateStopped, time.Now())
		}
	}
	r.stateMu.Lock()
	r.cleanupPending = true
	r.stateMu.Unlock()
	if err := r.SaveMetadata(); err != nil {
		return err
	}
	return m.completeCleanup(r)
}

func (m *Manager) completeCleanup(r *Run) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m.cleanupResources(ctx, r)
	r.stateMu.Lock()
	r.cleanupPending = false
	r.stateMu.Unlock()
	return r.SaveMetadata()
}

func readRunMetadata(r *Run) (storage.Metadata, error) {
	meta, err := r.Store.LoadMetadata()
	if errors.Is(err, fs.ErrNotExist) {
		return meta, fmt.Errorf("%w: %s", ErrRunNotFound, r.ID)
	}
	return meta, err
}

// Apply the owner's result to this manager's snapshot, including legacy
// failed records whose empty Error historically made Wait return nil.
func applyTerminalMetadata(r *Run, meta storage.Metadata) bool {
	state := State(meta.State)
	if meta.CleanupPending || (state != StateStopped && state != StateFailed) {
		return false
	}
	r.stateMu.Lock()
	r.State, r.Error, r.StoppedAt, r.ProviderMeta = state, meta.Error, meta.StoppedAt, meta.ProviderMeta
	r.stateMu.Unlock()
	return true
}

func (m *Manager) waitForPersistedRun(ctx context.Context, r *Run) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		done, err := m.pollOrRecoverRun(ctx, r)
		if err != nil || done {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *Manager) pollOrRecoverRun(ctx context.Context, r *Run) (bool, error) {
	meta, err := readRunMetadata(r)
	if err != nil {
		return false, err
	}
	if applyTerminalMetadata(r, meta) {
		return true, nil
	}

	// An absent lock file identifies a pre-protocol owner. Never acquire a
	// lock on its behalf: the older process could still be finalizing.
	lock := ownerLock(r.Store.Dir())
	_, err = os.Stat(lock.Path())
	if errors.Is(err, fs.ErrNotExist) {
		return m.observeLegacyRun(ctx, r)
	}
	if err != nil {
		return false, err
	}

	held, err := lock.TryLock()
	if err != nil || !held {
		return false, err
	}
	defer func() { _ = lock.Unlock() }()
	return m.recoverRunWithOwnerLock(ctx, r)
}

// Old owners do not participate in locking. Only update this observer's
// snapshot, even if the runtime reports that the guest has exited.
func (m *Manager) observeLegacyRun(ctx context.Context, r *Run) (bool, error) {
	rt, err := m.runtimeForRun(r)
	if err != nil {
		return false, err
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := rt.ContainerState(probe, r.ContainerID)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err == nil && !containerExited(state) {
		return false, nil
	}
	if latest, e := readRunMetadata(r); e != nil {
		return false, e
	} else if applyTerminalMetadata(r, latest) {
		return true, nil
	}
	if rt.Type() != container.RuntimeApple && err == nil {
		code, waitErr := rt.WaitContainer(probe, r.ContainerID)
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if waitErr != nil {
			return false, nil
		}
		if code != 0 {
			msg := fmt.Sprintf("exit code %d", code)
			r.SetStateWithError(StateFailed, msg)
			return true, nil
		}
	}
	r.SetState(StateStopped)
	return true, nil
}

// The caller holds owner.lock for this entire operation. Reload the inventory
// and state because the previous owner may have published its final result
// between the initial observation and lock acquisition.
func (m *Manager) recoverRunWithOwnerLock(ctx context.Context, r *Run) (bool, error) {
	meta, err := readRunMetadata(r)
	if err != nil {
		return false, err
	}
	if applyTerminalMetadata(r, meta) {
		return true, nil
	}
	if meta.CleanupPending {
		recovery := runFromMetadata(State(meta.State), meta, r.Store, r.ID, meta.ServiceContainers)
		if err = m.completeCleanup(recovery); err != nil {
			return false, err
		}
		meta.CleanupPending = false
		return applyTerminalMetadata(r, meta), nil
	}

	rt, err := m.runtimeForRun(r)
	if err != nil {
		return false, err
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := rt.ContainerState(probe, r.ContainerID)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	abandonedCreate := state == "created"
	// Startup includes firewall installation. A running guest with an
	// abandoned startup lease cannot be assumed to have finished that step.
	interruptedStart := meta.State == string(StateStarting) && state == "running" && err == nil
	if interruptedStart {
		if meta.Error == "" {
			meta.Error = "owner exited during container startup"
			if saveErr := r.Store.SaveMetadata(meta); saveErr != nil {
				return false, saveErr
			}
		}
		stopCtx, stopCancel := context.WithTimeout(ctx, 30*time.Second)
		stopErr := rt.StopContainer(stopCtx, r.ContainerID)
		stopCancel()
		if stopErr != nil || ctx.Err() != nil {
			return false, ctx.Err() // Retry transient failures on the next poll.
		}
	} else if err == nil && !abandonedCreate && !containerExited(state) {
		return false, nil
	}
	// We hold the dead owner's lock and have confirmed termination. Runtime
	// transport errors are retryable; cancellation never finalizes a run.
	var code int64
	var exitErr error
	switch {
	case meta.Error != "":
		exitErr = errors.New(meta.Error)
	case abandonedCreate:
		exitErr = errors.New("owner exited before the container started")
	case errors.Is(err, fs.ErrNotExist):
		exitErr = errors.New("container removed before its exit status was recorded")
	case rt.Type() == container.RuntimeApple:
		exitErr = errors.New("exit status unknown: Apple container stopped after losing its owner")
	default:
		code, err = rt.WaitContainer(probe, r.ContainerID)
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if err != nil {
			return false, nil
		}
	}
	// Use the persisted resource inventory, not the observer's reconciled
	// (and potentially partial) service list or inferred stopped state.
	recovery := runFromMetadata(State(meta.State), meta, r.Store, r.ID, meta.ServiceContainers)
	if err = m.finishRun(recovery, code, exitErr); err != nil {
		return false, err
	}
	final, err := readRunMetadata(r)
	if err != nil {
		return false, err
	}
	return applyTerminalMetadata(r, final), nil
}

func stopLock(r *Run) *flock.Flock {
	return flock.New(filepath.Join(r.Store.Dir(), "stop.lock"), flock.SetPermissions(0o600))
}

// Serialize stop requests across clients. Record acceptance only after the
// runtime confirms stop. The finalizer reads the marker under this same lock.
func (m *Manager) requestStop(ctx context.Context, r *Run) (bool, error) {
	if r.Store != nil {
		lock := stopLock(r)
		held, err := lock.TryLockContext(ctx, 25*time.Millisecond)
		if err != nil {
			return false, err
		}
		if !held {
			return false, ctx.Err()
		}
		defer func() { _ = lock.Unlock() }()
	}
	rt, err := m.runtimeForRun(r)
	if err != nil {
		return false, err
	}
	state, inspectErr := rt.ContainerState(ctx, r.ContainerID)
	if errors.Is(inspectErr, fs.ErrNotExist) || (inspectErr == nil && containerExited(state)) {
		return false, nil
	}
	if err := rt.StopContainer(ctx, r.ContainerID); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		state, e := rt.ContainerState(ctx, r.ContainerID)
		if !errors.Is(e, fs.ErrNotExist) && (e != nil || !containerExited(state)) {
			return false, err
		}
	}
	if r.Store != nil {
		if err := os.WriteFile(filepath.Join(r.Store.Dir(), "stop.request"), nil, 0o600); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (m *Manager) stopObserved(ctx context.Context, r *Run) error {
	meta, err := readRunMetadata(r)
	if err != nil {
		return err
	}
	if applyTerminalMetadata(r, meta) {
		return nil
	}
	stopCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for State(meta.State) == StateStarting || State(meta.State) == StateCreated {
		done, e := m.pollOrRecoverRun(stopCtx, r)
		if done || e != nil {
			return e
		}
		if State(meta.State) == StateCreated {
			return fmt.Errorf("run %s has not started", r.ID)
		}
		select {
		case <-stopCtx.Done():
			return stopCtx.Err()
		case <-time.After(50 * time.Millisecond):
		}
		meta, err = readRunMetadata(r)
		if err != nil {
			return err
		}
		if applyTerminalMetadata(r, meta) {
			return nil
		}
	}
	if !meta.CleanupPending {
		if _, err := m.requestStop(stopCtx, r); err != nil {
			return err
		}
	}
	if _, err := os.Stat(ownerLock(r.Store.Dir()).Path()); errors.Is(err, fs.ErrNotExist) {
		// A pre-protocol owner's explicit stop retains the legacy direct cleanup path.
		r.SetState(StateStopping)
		return m.finishRun(r, 0, nil)
	}
	return m.waitForPersistedRun(stopCtx, r)
}
