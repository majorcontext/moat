# Multi-Runtime Manager Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make all moat CLI commands (status, stop, destroy, exec, clean, etc.) work correctly when runs exist across both Docker and Apple container runtimes on the same machine.

**Architecture:** Replace the single `container.Runtime` in `Manager` with a `RuntimePool` that lazily initializes runtimes by type. Each run records which runtime created it (already done in PR #309). Operations on existing runs resolve the correct runtime from the pool via `Run.Runtime`. New run creation uses the auto-detected default runtime. Commands that query infrastructure directly (`moat status`, `moat clean`, `moat system images/containers`) query all available runtimes and merge results.

**Tech Stack:** Go, Docker SDK, Apple `container` CLI

---

## File Structure

| File | Responsibility |
|------|---------------|
| `internal/container/pool.go` (new) | `RuntimePool` — manages multiple runtime instances, lazily initialized, keyed by `RuntimeType`. Thread-safe. Single source of truth for "give me a runtime for this type." |
| `internal/container/pool_test.go` (new) | Tests for `RuntimePool` |
| `internal/container/detect.go` (modify) | Add `NewRuntimeByType(RuntimeType, RuntimeOptions) (Runtime, error)` factory |
| `internal/container/runtime.go` (modify) | Add `AllRuntimeTypes()` helper |
| `internal/run/manager.go` (modify) | Replace `runtime container.Runtime` with `runtimePool *container.RuntimePool`. Add `runtimeForRun(r *Run)` helper. Update all `m.runtime.X()` call sites. |
| `internal/run/manager_test.go` (modify) | Update `stubRuntime` and tests to work with pool |
| `cmd/moat/cli/status.go` (modify) | Query all available runtimes for images/containers |
| `cmd/moat/cli/clean.go` (modify) | Query all available runtimes for images/containers/networks |
| `cmd/moat/cli/system_containers.go` (modify) | Query all available runtimes |
| `cmd/moat/cli/system_images.go` (modify) | Query all available runtimes |

---

### Task 1: RuntimePool — the multi-runtime abstraction

The core new type. A thread-safe pool that holds at most one runtime per `RuntimeType`, lazily initialized on first access. This replaces all direct `container.NewRuntime()` calls with a single shared pool.

**Files:**
- Create: `internal/container/pool.go`
- Create: `internal/container/pool_test.go`
- Modify: `internal/container/detect.go`
- Modify: `internal/container/runtime.go`

- [ ] **Step 1: Add `AllRuntimeTypes()` to `runtime.go`**

Add after the `RuntimeApple` constant block in `internal/container/runtime.go`:

```go
// AllRuntimeTypes returns all known runtime types.
func AllRuntimeTypes() []RuntimeType {
	return []RuntimeType{RuntimeDocker, RuntimeApple}
}
```

- [ ] **Step 2: Add `NewRuntimeByType` factory to `detect.go`**

Add at the end of `internal/container/detect.go`:

```go
// NewRuntimeByType creates a runtime of the specified type.
// Returns an error if the runtime is not available on this system.
func NewRuntimeByType(rt RuntimeType, opts RuntimeOptions) (Runtime, error) {
	switch rt {
	case RuntimeDocker:
		return newDockerRuntimeWithPing(opts.Sandbox)
	case RuntimeApple:
		r, reason := tryAppleRuntime()
		if r != nil {
			return r, nil
		}
		return nil, fmt.Errorf("Apple container runtime not available: %s", reason)
	default:
		return nil, fmt.Errorf("unknown runtime type: %q", rt)
	}
}
```

- [ ] **Step 3: Run existing tests to verify no breakage**

Run: `go test ./internal/container/... -count=1`
Expected: PASS (new functions are additive)

- [ ] **Step 4: Write failing test for `RuntimePool`**

Create `internal/container/pool_test.go`:

```go
package container

import (
	"testing"
)

func TestRuntimePoolGetDefault(t *testing.T) {
	pool, err := NewRuntimePool(RuntimeOptions{Sandbox: false})
	if err != nil {
		t.Fatalf("NewRuntimePool: %v", err)
	}
	defer pool.Close()

	// Default runtime should be available
	rt := pool.Default()
	if rt == nil {
		t.Fatal("Default() returned nil")
	}
	if rt.Type() != RuntimeDocker && rt.Type() != RuntimeApple {
		t.Fatalf("unexpected default runtime type: %s", rt.Type())
	}
}

func TestRuntimePoolGet(t *testing.T) {
	pool, err := NewRuntimePool(RuntimeOptions{Sandbox: false})
	if err != nil {
		t.Fatalf("NewRuntimePool: %v", err)
	}
	defer pool.Close()

	defaultType := pool.Default().Type()

	// Getting the default type should return the same instance
	rt, err := pool.Get(defaultType)
	if err != nil {
		t.Fatalf("Get(%s): %v", defaultType, err)
	}
	if rt != pool.Default() {
		t.Fatal("Get(default type) returned different instance than Default()")
	}
}

func TestRuntimePoolGetUnknownType(t *testing.T) {
	pool, err := NewRuntimePool(RuntimeOptions{Sandbox: false})
	if err != nil {
		t.Fatalf("NewRuntimePool: %v", err)
	}
	defer pool.Close()

	_, err = pool.Get("unknown")
	if err == nil {
		t.Fatal("expected error for unknown runtime type")
	}
}

func TestRuntimePoolAvailable(t *testing.T) {
	pool, err := NewRuntimePool(RuntimeOptions{Sandbox: false})
	if err != nil {
		t.Fatalf("NewRuntimePool: %v", err)
	}
	defer pool.Close()

	runtimes := pool.Available()
	if len(runtimes) == 0 {
		t.Fatal("Available() returned empty list")
	}

	// The default runtime must be in the available list
	found := false
	for _, rt := range runtimes {
		if rt.Type() == pool.Default().Type() {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("default runtime not in Available() list")
	}
}

func TestRuntimePoolCloseIdempotent(t *testing.T) {
	pool, err := NewRuntimePool(RuntimeOptions{Sandbox: false})
	if err != nil {
		t.Fatalf("NewRuntimePool: %v", err)
	}

	if err := pool.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
```

- [ ] **Step 5: Run test to verify it fails**

Run: `go test ./internal/container/ -run TestRuntimePool -count=1`
Expected: FAIL — `NewRuntimePool` doesn't exist

- [ ] **Step 6: Implement `RuntimePool`**

Create `internal/container/pool.go`:

```go
package container

import (
	"fmt"
	"sync"
)

// RuntimePool manages multiple container runtime instances, keyed by RuntimeType.
// It lazily initializes runtimes on first access and provides a default runtime
// for new run creation. Thread-safe for concurrent access.
type RuntimePool struct {
	mu       sync.Mutex
	runtimes map[RuntimeType]Runtime
	dflt     Runtime
	opts     RuntimeOptions
	closed   bool
}

// NewRuntimePool creates a pool with the auto-detected default runtime.
// The default runtime is initialized immediately; other runtimes are
// created lazily when first requested via Get().
func NewRuntimePool(opts RuntimeOptions) (*RuntimePool, error) {
	rt, err := NewRuntimeWithOptions(opts)
	if err != nil {
		return nil, err
	}

	pool := &RuntimePool{
		runtimes: map[RuntimeType]Runtime{rt.Type(): rt},
		dflt:     rt,
		opts:     opts,
	}
	return pool, nil
}

// NewRuntimePoolWithDefault creates a pool with a pre-existing runtime as default.
// Used in tests to inject a stub runtime.
func NewRuntimePoolWithDefault(rt Runtime) *RuntimePool {
	return &RuntimePool{
		runtimes: map[RuntimeType]Runtime{rt.Type(): rt},
		dflt:     rt,
	}
}

// Default returns the auto-detected default runtime.
// Used for creating new runs.
func (p *RuntimePool) Default() Runtime {
	return p.dflt
}

// Get returns the runtime for the given type, lazily initializing it if needed.
// Returns the default runtime if typ is empty (legacy runs without a runtime field).
func (p *RuntimePool) Get(typ RuntimeType) (Runtime, error) {
	if typ == "" {
		return p.dflt, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, fmt.Errorf("runtime pool is closed")
	}

	if rt, ok := p.runtimes[typ]; ok {
		return rt, nil
	}

	rt, err := NewRuntimeByType(typ, p.opts)
	if err != nil {
		return nil, fmt.Errorf("runtime %s not available: %w", typ, err)
	}

	p.runtimes[typ] = rt
	return rt, nil
}

// Available returns all runtimes that are currently initialized in the pool.
// Does not probe for new runtimes — only returns what has been lazily created
// via Get() or the default from construction.
func (p *RuntimePool) Available() []Runtime {
	p.mu.Lock()
	defer p.mu.Unlock()

	rts := make([]Runtime, 0, len(p.runtimes))
	for _, rt := range p.runtimes {
		rts = append(rts, rt)
	}
	return rts
}

// ForEachAvailable calls fn for each runtime type, initializing it if possible.
// Errors from unavailable runtimes are silently skipped — fn is only called
// for runtimes that can be successfully initialized.
// Used by status/clean commands that need to query all runtimes.
func (p *RuntimePool) ForEachAvailable(fn func(Runtime) error) error {
	for _, typ := range AllRuntimeTypes() {
		rt, err := p.Get(typ)
		if err != nil {
			continue // Runtime not available on this system
		}
		if err := fn(rt); err != nil {
			return err
		}
	}
	return nil
}

// Close closes all runtimes in the pool.
func (p *RuntimePool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}
	p.closed = true

	var firstErr error
	for _, rt := range p.runtimes {
		if err := rt.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/container/ -run TestRuntimePool -count=1`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/container/pool.go internal/container/pool_test.go internal/container/detect.go internal/container/runtime.go
git commit -m "feat(container): add RuntimePool for multi-runtime support

Introduces RuntimePool, a thread-safe pool that manages multiple container
runtime instances keyed by RuntimeType. Runtimes are lazily initialized
on first access. Includes NewRuntimeByType factory and ForEachAvailable
for querying all available runtimes."
```

---

### Task 2: Migrate Manager to RuntimePool

Replace `m.runtime` with `m.runtimePool` and add a `runtimeForRun` helper. This is the core refactor — every `m.runtime.X()` call becomes either `m.runtimePool.Default().X()` (for new run creation) or uses the pool to resolve the correct runtime for an existing run.

**Files:**
- Modify: `internal/run/manager.go`

- [ ] **Step 1: Change Manager struct and constructor**

In `internal/run/manager.go`, replace the `runtime` field in the Manager struct (line 85):

```go
// Before:
runtime        container.Runtime

// After:
runtimePool    *container.RuntimePool
```

Replace the constructor `NewManagerWithOptions` (lines 110-155). Change lines 120-123:

```go
// Before:
rt, err := container.NewRuntimeWithOptions(runtimeOpts)
if err != nil {
    return nil, fmt.Errorf("initializing container runtime: %w", err)
}

// After:
pool, err := container.NewRuntimePool(runtimeOpts)
if err != nil {
    return nil, fmt.Errorf("initializing container runtime: %w", err)
}
```

Change line 137:

```go
// Before:
runtime:        rt,

// After:
runtimePool:    pool,
```

- [ ] **Step 2: Add `runtimeForRun` helper**

Add after the Manager struct definition (around line 100):

```go
// runtimeForRun returns the correct container runtime for an existing run.
// It uses the run's Runtime field to look up the matching runtime from the pool.
// For legacy runs without a Runtime field, falls back to the default runtime.
func (m *Manager) runtimeForRun(r *Run) (container.Runtime, error) {
	return m.runtimePool.Get(container.RuntimeType(r.Runtime))
}
```

- [ ] **Step 3: Update `RuntimeType()` and `Close()` methods**

Update `RuntimeType()` (line 3628):

```go
// Before:
func (m *Manager) RuntimeType() string {
    return string(m.runtime.Type())
}

// After:
func (m *Manager) RuntimeType() string {
    return string(m.runtimePool.Default().Type())
}
```

Update `Close()` (line 3668):

```go
// Before:
return m.runtime.Close()

// After:
return m.runtimePool.Close()
```

- [ ] **Step 4: Update `Create()` — use `runtimePool.Default()` for new runs**

In the `Create()` method, replace all `m.runtime.` calls with `m.runtimePool.Default().` since `Create` always uses the default runtime for new runs. These are on lines:

- 875, 1115, 1131, 1224, 1235, 1363, 1548, 1554, 1557, 1575, 1621, 1624, 1653, 1765
- 2092, 2098, 2139, 2141, 2149, 2154, 2177, 2178, 2199, 2227, 2433, 2473, 2479
- 2504, 2505, 2506, 2545, 2555, 2571, 2594

Use a global find-and-replace within the `Create()` method body only. Replace:
```
m.runtime.
```
with:
```
m.runtimePool.Default().
```

Verify line 1548 still reads:
```go
r.Runtime = string(m.runtimePool.Default().Type())
```

- [ ] **Step 5: Update `Start()` and `StartAttached()` — use `runtimePool.Default()`**

These methods are called on newly created runs, so they use the default runtime. Replace `m.runtime.` with `m.runtimePool.Default().` in:

- `setupPortBindings()` (line 2718)
- `setupFirewall()` (lines 2761, 2763)
- `Start()` (line 2783)
- `StartAttached()` (lines 2896, 2955)

- [ ] **Step 6: Update `Stop()` — use `runtimeForRun()`**

This is the first method that may operate on a cross-runtime run. Replace the `m.runtime.StopContainer()` call at line 3016:

```go
// Before:
if err := m.runtime.StopContainer(ctx, r.ContainerID); err != nil {

// After:
rt, rtErr := m.runtimeForRun(r)
if rtErr != nil {
    return fmt.Errorf("resolving runtime for run %s: %w", runID, rtErr)
}
if err := rt.StopContainer(ctx, r.ContainerID); err != nil {
```

- [ ] **Step 7: Update `cleanupResources()` — use `runtimeForRun()`**

This method calls many runtime methods for stopping services, removing containers, and removing networks. Resolve the runtime once at the top of the `cleanupOnce.Do` closure and use it throughout.

At the start of the `cleanupOnce.Do` closure (around line 3213), add:

```go
rt, rtErr := m.runtimeForRun(r)
if rtErr != nil {
    log.Debug("cleanup: cannot resolve runtime, skipping container cleanup", "run", r.ID, "error", rtErr)
    // Still clean up non-container resources (proxy, SSH, routes, temp dirs) below
}
```

Then replace all `m.runtime.` inside `cleanupResources` with `rt.`, guarding container operations with `if rt != nil`:

```go
// Service containers (line 3231)
if rt != nil && len(r.ServiceContainers) > 0 {
    svcMgr := rt.ServiceManager()
    // ...
}

// BuildKit sidecar (line 3244)
if rt != nil && r.BuildkitContainerID != "" {
    // ...rt.StopContainer, rt.RemoveContainer...
}

// Main container (line 3255)
if rt != nil && !r.KeepContainer {
    if err := rt.RemoveContainer(ctx, r.ContainerID); err != nil {
        // ...
    }
}

// Network (line 3262)
if rt != nil && r.NetworkID != "" {
    netMgr := rt.NetworkManager()
    // ...
}
```

Non-container cleanup (proxy, SSH agent, routes, temp dirs, provider paths) stays unconditional — those don't need the container runtime.

- [ ] **Step 8: Update `Exec()`, `ResizeTTY()`, `WriteClipboard()` — use `runtimeForRun()`**

`Exec()` at line 3544:

```go
// Before:
execErr := m.runtime.Exec(ctx, containerID, cmd, stdin, stdout, stderr)

// After:
rt, rtErr := m.runtimeForRun(r)
if rtErr != nil {
    return fmt.Errorf("resolving runtime: %w", rtErr)
}
execErr := rt.Exec(ctx, containerID, cmd, stdin, stdout, stderr)
```

Note: the `r` variable needs to be captured before `m.mu.RUnlock()`. It's already done — `r` is read at line 3530.

`ResizeTTY()` at line 3494:

```go
// Before:
return m.runtime.ResizeTTY(ctx, containerID, height, width)

// After:
rt, rtErr := m.runtimeForRun(r)
if rtErr != nil {
    return fmt.Errorf("resolving runtime: %w", rtErr)
}
return rt.ResizeTTY(ctx, containerID, height, width)
```

Note: need to keep `r` in scope — it's already available at line 3487.

- [ ] **Step 9: Update `FollowLogs()` and `RecentLogs()` — use `runtimeForRun()`**

`FollowLogs()` at line 3574:

```go
// Before:
logs, err := m.runtime.ContainerLogs(ctx, containerID)

// After:
rt, rtErr := m.runtimeForRun(r)
if rtErr != nil {
    return fmt.Errorf("resolving runtime: %w", rtErr)
}
logs, err := rt.ContainerLogs(ctx, containerID)
```

`RecentLogs()` at line 3597:

```go
// Before:
allLogs, err := m.runtime.ContainerLogsAll(context.Background(), containerID)

// After:
rt, rtErr := m.runtimeForRun(r)
if rtErr != nil {
    return "", fmt.Errorf("resolving runtime: %w", rtErr)
}
allLogs, err := rt.ContainerLogsAll(context.Background(), containerID)
```

- [ ] **Step 10: Update `captureLogs()` — use `runtimeForRun()`**

At line 3101, change the Apple runtime type check:

```go
// Before:
if r.Interactive && m.runtime.Type() == container.RuntimeApple {

// After:
if r.Interactive && container.RuntimeType(r.Runtime) == container.RuntimeApple {
```

At line 3130:

```go
// Before:
allLogs, logErr := m.runtime.ContainerLogsAll(logCtx, r.ContainerID)

// After:
rt, rtErr := m.runtimeForRun(r)
if rtErr != nil {
    log.Debug("cannot resolve runtime for log capture", "run", r.ID, "error", rtErr)
    return
}
allLogs, logErr := rt.ContainerLogsAll(logCtx, r.ContainerID)
```

- [ ] **Step 11: Update `monitorContainerExit()` — use `runtimeForRun()`**

At line 3312:

```go
// Before:
exitCode, err := m.runtime.WaitContainer(context.Background(), r.ContainerID)

// After:
rt, rtErr := m.runtimeForRun(r)
if rtErr != nil {
    log.Debug("cannot resolve runtime for container monitor", "run", r.ID, "error", rtErr)
    r.SetStateFailedAt("runtime unavailable: "+rtErr.Error(), time.Now())
    _ = r.SaveMetadata()
    close(r.exitCh)
    return
}
exitCode, err := rt.WaitContainer(context.Background(), r.ContainerID)
```

This also removes the need for the `crossRuntime` guard at line 400 in `registerPersistedRun`. Since `runtimeForRun` will resolve the correct runtime for any run, `monitorContainerExit` can now be spawned for all running containers regardless of runtime type:

```go
// Before (line 397-403):
crossRuntime := meta.Runtime != "" && meta.Runtime != string(m.runtime.Type())
if runState == StateRunning && !crossRuntime {
    go m.monitorContainerExit(r)
}

// After:
if runState == StateRunning {
    m.monitorWg.Add(1)
    go func() {
        defer m.monitorWg.Done()
        m.monitorContainerExit(r)
    }()
}
```

Wait — check the existing code at lines 401-403 to see if the `monitorWg.Add/Done` is already there or if it's handled inside `monitorContainerExit`. Read the full method to confirm.

Looking at `registerPersistedRun` lines 397-403:

```go
crossRuntime := meta.Runtime != "" && meta.Runtime != string(m.runtime.Type())
if runState == StateRunning && !crossRuntime {
    m.monitorWg.Add(1)
    go func() {
        defer m.monitorWg.Done()
        m.monitorContainerExit(r)
    }()
}
```

The `monitorWg` is handled at the call site. Remove the `crossRuntime` guard and keep the `monitorWg` wrapping:

```go
if runState == StateRunning {
    m.monitorWg.Add(1)
    go func() {
        defer m.monitorWg.Done()
        m.monitorContainerExit(r)
    }()
}
```

- [ ] **Step 12: Update `loadPersistedRuns()` — use pool for cross-runtime state checks**

The cross-runtime skip at lines 242-250 is no longer needed. The pool will lazily initialize the correct runtime. Replace:

```go
// Before (lines 242-250):
if info.meta.Runtime != "" && info.meta.Runtime != string(m.runtime.Type()) {
    log.Debug("skipping cross-runtime container check, preserving persisted state", ...)
    results[idx] = checkedRun{
        info:              info,
        runState:          State(info.meta.State),
        serviceContainers: info.meta.ServiceContainers,
    }
    return
}

containerState, csErr := m.runtime.ContainerState(callCtx, info.meta.ContainerID)

// After:
rt, rtErr := m.runtimePool.Get(container.RuntimeType(info.meta.Runtime))
if rtErr != nil {
    log.Debug("runtime not available, preserving persisted state",
        "id", info.runID, "runtime", info.meta.Runtime, "error", rtErr)
    results[idx] = checkedRun{
        info:              info,
        runState:          State(info.meta.State),
        serviceContainers: info.meta.ServiceContainers,
    }
    return
}

containerState, csErr := rt.ContainerState(callCtx, info.meta.ContainerID)
```

Similarly update the service container state check at line 292:

```go
// Before:
if _, scErr := m.runtime.ContainerState(svcCtx, id); scErr == nil {

// After:
if _, scErr := rt.ContainerState(svcCtx, id); scErr == nil {
```

- [ ] **Step 13: Verify compilation**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 14: Run all unit tests**

Run: `make test-unit`
Expected: Some test failures in `manager_test.go` (tests use `stubRuntime` which needs updating — addressed in Task 3)

- [ ] **Step 15: Commit**

```bash
git add internal/run/manager.go
git commit -m "refactor(run): migrate Manager from single runtime to RuntimePool

Replace m.runtime with m.runtimePool throughout Manager. Operations on
existing runs use runtimeForRun() to resolve the correct runtime from
the pool. New run creation uses runtimePool.Default().

This enables cross-runtime stop, destroy, exec, logs, and cleanup —
a Docker Manager can now operate on Apple container runs and vice versa.

Removes the cross-runtime skip guard from loadPersistedRuns and the
crossRuntime guard from monitorContainerExit, since the pool lazily
initializes the correct runtime on demand."
```

---

### Task 3: Update manager tests for RuntimePool

The existing tests use `stubRuntime` and construct a Manager with a direct `runtime` field assignment. These need to use `NewRuntimePoolWithDefault(stub)` instead.

**Files:**
- Modify: `internal/run/manager_test.go`

- [ ] **Step 1: Find all test setup patterns**

Search for `m.runtime` or `runtime:` in `manager_test.go`. Every test that creates a Manager manually needs to change from:

```go
m := &Manager{
    runtime: stub,
    runs:    make(map[string]*Run),
    // ...
}
```

to:

```go
m := &Manager{
    runtimePool: container.NewRuntimePoolWithDefault(stub),
    runs:        make(map[string]*Run),
    // ...
}
```

- [ ] **Step 2: Update all Manager construction in tests**

Replace every occurrence of `runtime: stub` (or `runtime: &stubRuntime{...}`) with `runtimePool: container.NewRuntimePoolWithDefault(stub)`.

Add the import `"github.com/majorcontext/moat/internal/container"` if not already present.

- [ ] **Step 3: Update `TestLoadPersistedRunsSkipsCrossRuntimeCheck`**

This test verifies that a run created with a different runtime is handled correctly. With the pool, the behavior changes: instead of skipping the check entirely, the pool will try to initialize the other runtime and fail (since only the stub is available). The fallback behavior (preserve persisted state) is the same.

The test should still pass as-is because `runtimePool.Get("docker")` when the stub is "apple" (or vice versa) will fail with "runtime not available", which triggers the same preserve-persisted-state path.

Verify the test still sets `meta.Runtime` to a value different from the stub's `Type()` return value.

- [ ] **Step 4: Run all tests**

Run: `make test-unit`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/run/manager_test.go
git commit -m "test(run): update manager tests for RuntimePool

Replace direct runtime field assignment with
NewRuntimePoolWithDefault(stub) in all test Manager construction."
```

---

### Task 4: Multi-runtime `moat status`

The `status` command currently creates its own `container.Runtime` directly (not via Manager) to list images and containers. It needs to query all available runtimes.

**Files:**
- Modify: `cmd/moat/cli/status.go`

- [ ] **Step 1: Replace single runtime with RuntimePool**

In `showStatus()` (line 68), replace:

```go
// Before (lines 71-76):
rt, err := container.NewRuntimeWithOptions(container.RuntimeOptions{Sandbox: false})
if err != nil {
    return fmt.Errorf("initializing runtime: %w", err)
}
defer rt.Close()

// After:
pool, err := container.NewRuntimePool(container.RuntimeOptions{Sandbox: false})
if err != nil {
    return fmt.Errorf("initializing runtime: %w", err)
}
defer pool.Close()
```

- [ ] **Step 2: Query all runtimes for images**

Replace lines 93-97:

```go
// Before:
images, err := rt.ListImages(ctx)
if err != nil {
    return fmt.Errorf("listing images: %w", err)
}

// After:
var images []container.ImageInfo
pool.ForEachAvailable(func(rt container.Runtime) error {
    rtImages, err := rt.ListImages(ctx)
    if err != nil {
        log.Debug("listing images failed", "runtime", rt.Type(), "error", err)
        return nil // Continue to next runtime
    }
    images = append(images, rtImages...)
    return nil
})
```

- [ ] **Step 3: Query all runtimes for orphan container check**

Replace lines 178-202:

```go
// Before:
containers, err := rt.ListContainers(ctx)
if err != nil {
    // ...
} else {
    // orphan check
}

// After:
var allContainers []container.Info
pool.ForEachAvailable(func(rt container.Runtime) error {
    cs, err := rt.ListContainers(ctx)
    if err != nil {
        output.Health = append(output.Health, healthItem{
            Status:  "warning",
            Message: fmt.Sprintf("Failed to list %s containers: %v", rt.Type(), err),
        })
        return nil
    }
    allContainers = append(allContainers, cs...)
    return nil
})
if len(allContainers) > 0 {
    knownRunIDs := make(map[string]bool)
    for _, r := range runs {
        knownRunIDs[r.ID] = true
    }
    orphanedCount := 0
    for _, c := range allContainers {
        if !knownRunIDs[c.Name] {
            orphanedCount++
        }
    }
    if orphanedCount > 0 {
        output.Health = append(output.Health, healthItem{
            Status:  "warning",
            Message: fmt.Sprintf("%d orphaned containers", orphanedCount),
        })
    }
}
```

- [ ] **Step 4: Update runtime display**

Replace lines 124-126:

```go
// Before:
output := statusOutput{
    Runtime: string(rt.Type()),
}

// After:
var runtimeNames []string
pool.ForEachAvailable(func(rt container.Runtime) error {
    runtimeNames = append(runtimeNames, string(rt.Type()))
    return nil
})
output := statusOutput{
    Runtime: strings.Join(runtimeNames, ", "),
}
```

Add `"strings"` to the import block if not already present.

- [ ] **Step 5: Verify compilation and test**

Run: `go build ./cmd/moat/ && go vet ./cmd/moat/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add cmd/moat/cli/status.go
git commit -m "feat(status): query all available runtimes for images and containers

moat status now shows images, containers, and orphan checks across
both Docker and Apple runtimes when both are available."
```

---

### Task 5: Multi-runtime `moat clean`

Same pattern as status — query all runtimes for images, containers, and networks.

**Files:**
- Modify: `cmd/moat/cli/clean.go`

- [ ] **Step 1: Replace single runtime with RuntimePool**

Replace lines 46-50:

```go
// Before:
rt, err := container.NewRuntimeWithOptions(container.RuntimeOptions{Sandbox: false})
if err != nil {
    return fmt.Errorf("initializing runtime: %w", err)
}
defer rt.Close()

// After:
pool, err := container.NewRuntimePool(container.RuntimeOptions{Sandbox: false})
if err != nil {
    return fmt.Errorf("initializing runtime: %w", err)
}
defer pool.Close()
```

- [ ] **Step 2: Query all runtimes for images**

Replace lines 82-86 (image listing):

```go
// Before:
images, err := rt.ListImages(ctx)
if err != nil {
    return fmt.Errorf("listing images: %w", err)
}

// After:
type runtimeImage struct {
    image container.ImageInfo
    rt    container.Runtime
}
var allImages []runtimeImage
pool.ForEachAvailable(func(rt container.Runtime) error {
    imgs, err := rt.ListImages(ctx)
    if err != nil {
        ui.Warnf("Failed to list %s images: %v", rt.Type(), err)
        return nil
    }
    for _, img := range imgs {
        allImages = append(allImages, runtimeImage{image: img, rt: rt})
    }
    return nil
})
// Rebuild the images slice for the existing code below
images := make([]container.ImageInfo, len(allImages))
for i, ri := range allImages {
    images[i] = ri.image
}
```

- [ ] **Step 3: Query all runtimes for container in-use check**

Replace lines 97-113 (container listing for in-use check):

```go
// Before:
containers, containerErr := rt.ListContainers(ctx)
runningImages := make(map[string]bool)
if containerErr == nil {
    for _, c := range containers {
        if c.Status == "running" {
            runningImages[c.Image] = true
            if id, ok := tagToID[c.Image]; ok {
                runningImages[id] = true
            }
        }
    }
} else {
    ui.Warnf("Failed to list containers: %v", containerErr)
    ui.Info("Images will be skipped if running containers cannot be verified")
}

// After:
runningImages := make(map[string]bool)
var containerListFailed bool
pool.ForEachAvailable(func(rt container.Runtime) error {
    containers, err := rt.ListContainers(ctx)
    if err != nil {
        ui.Warnf("Failed to list %s containers: %v", rt.Type(), err)
        containerListFailed = true
        return nil
    }
    for _, c := range containers {
        if c.Status == "running" {
            runningImages[c.Image] = true
            if id, ok := tagToID[c.Image]; ok {
                runningImages[id] = true
            }
        }
    }
    return nil
})
if containerListFailed {
    ui.Info("Images will be skipped if running containers cannot be verified")
}
```

- [ ] **Step 4: Query all runtimes for orphaned networks**

Replace lines 123-144:

```go
// Before:
var orphanedNetworks []container.NetworkInfo
netMgr := rt.NetworkManager()
if netMgr != nil {
    // ...
}

// After:
type runtimeNetwork struct {
    network container.NetworkInfo
    rt      container.Runtime
}
var orphanedNetworks []runtimeNetwork
pool.ForEachAvailable(func(rt container.Runtime) error {
    netMgr := rt.NetworkManager()
    if netMgr == nil {
        return nil
    }
    allNetworks, netErr := netMgr.ListNetworks(ctx)
    if netErr != nil {
        ui.Warnf("Failed to list %s networks: %v", rt.Type(), netErr)
        return nil
    }
    knownNetworkIDs := make(map[string]bool)
    for _, r := range runs {
        if r.NetworkID != "" {
            knownNetworkIDs[r.NetworkID] = true
        }
    }
    for _, n := range allNetworks {
        if !knownNetworkIDs[n.ID] {
            orphanedNetworks = append(orphanedNetworks, runtimeNetwork{network: n, rt: rt})
        }
    }
    return nil
})
```

- [ ] **Step 5: Update image removal to use correct runtime**

Replace lines 258-275 (image removal loop) to use the tracked runtime:

```go
// Before:
for _, img := range unusedImages {
    // ...
    if isImageInUse(ctx, rt, img.ID, img.Tag) {
    // ...
    if err := rt.RemoveImage(ctx, img.ID); err != nil {

// After — use allImages with runtime tracking:
for _, ri := range unusedRuntimeImages {
    img := ri.image
    fmt.Printf("Removing image %s... ", img.Tag)

    if isImageInUse(ctx, ri.rt, img.ID, img.Tag) {
        fmt.Println(ui.Yellow("skipped (now in use)"))
        continue
    }

    if err := ri.rt.RemoveImage(ctx, img.ID); err != nil {
```

This requires building `unusedRuntimeImages` instead of `unusedImages` in the filtering step (lines 115-121):

```go
var unusedRuntimeImages []runtimeImage
for _, ri := range allImages {
    if !runningImages[ri.image.Tag] && !runningImages[ri.image.ID] {
        unusedRuntimeImages = append(unusedRuntimeImages, ri)
    }
}
// Keep unusedImages for display logic
unusedImages := make([]container.ImageInfo, len(unusedRuntimeImages))
for i, ri := range unusedRuntimeImages {
    unusedImages[i] = ri.image
}
```

- [ ] **Step 6: Update network removal to use correct runtime**

Replace lines 278-289 (network removal loop):

```go
// Before:
if netMgr != nil {
    for _, n := range orphanedNetworks {
        fmt.Printf("Removing network %s... ", n.Name)
        if err := netMgr.ForceRemoveNetwork(ctx, n.ID); err != nil {

// After:
for _, rn := range orphanedNetworks {
    fmt.Printf("Removing network %s... ", rn.network.Name)
    netMgr := rn.rt.NetworkManager()
    if netMgr == nil {
        continue
    }
    if err := netMgr.ForceRemoveNetwork(ctx, rn.network.ID); err != nil {
```

- [ ] **Step 7: Update `isImageInUse` helper**

The `isImageInUse` function at line 336 takes a single `rt`. To check across all runtimes, update it to accept the pool:

Actually, looking more carefully, `isImageInUse` is a race-condition guard called per-image. It should check the runtime that owns the image, which is already passed as `ri.rt`. The existing function signature works — no change needed.

- [ ] **Step 8: Update orphaned networks display and count**

The display loop and count logic reference `orphanedNetworks` fields. Update:

```go
// Display (around line 194):
if len(orphanedNetworks) > 0 {
    fmt.Printf("%s (%d):\n", ui.Bold("Orphaned networks"), len(orphanedNetworks))
    for _, rn := range orphanedNetworks {
        fmt.Printf("  %s\n", rn.network.Name)
    }
    fmt.Println()
}
```

- [ ] **Step 9: Verify compilation**

Run: `go build ./cmd/moat/ && go vet ./cmd/moat/...`
Expected: PASS

- [ ] **Step 10: Commit**

```bash
git add cmd/moat/cli/clean.go
git commit -m "feat(clean): query all available runtimes for images, containers, and networks

moat clean now discovers and removes resources from both Docker and Apple
runtimes. Each image and network tracks which runtime owns it so removal
uses the correct runtime."
```

---

### Task 6: Multi-runtime `moat system` commands

These commands create their own `container.Runtime` directly. They need to query both runtimes.

**Files:**
- Modify: `cmd/moat/cli/system_containers.go`
- Modify: `cmd/moat/cli/system_images.go`

- [ ] **Step 1: Update `system_containers.go`**

Replace the single runtime with a pool and merge container lists:

```go
func listSystemContainers(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	pool, err := container.NewRuntimePool(container.RuntimeOptions{Sandbox: false})
	if err != nil {
		return fmt.Errorf("initializing runtime: %w", err)
	}
	defer pool.Close()

	type runtimeContainer struct {
		info    container.Info
		rtType  container.RuntimeType
	}
	var all []runtimeContainer
	pool.ForEachAvailable(func(rt container.Runtime) error {
		containers, err := rt.ListContainers(ctx)
		if err != nil {
			ui.Warnf("Failed to list %s containers: %v", rt.Type(), err)
			return nil
		}
		for _, c := range containers {
			all = append(all, runtimeContainer{info: c, rtType: rt.Type()})
		}
		return nil
	})

	if jsonOut {
		// Flatten to plain container list for JSON
		containers := make([]container.Info, len(all))
		for i, rc := range all {
			containers[i] = rc.info
		}
		return json.NewEncoder(os.Stdout).Encode(containers)
	}

	if len(all) == 0 {
		fmt.Println("No moat containers found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CONTAINER ID\tNAME\tRUNTIME\tSTATUS\tCREATED")
	for _, rc := range all {
		c := rc.info
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			c.ID, c.Name, rc.rtType, c.Status, formatAge(c.Created))
	}
	w.Flush()

	fmt.Println()
	fmt.Println("To remove a container:")
	fmt.Println("  docker rm <container-id>     (Docker)")
	fmt.Println("  container rm <container-id>   (Apple)")

	return nil
}
```

Add `"github.com/majorcontext/moat/internal/ui"` to imports.

- [ ] **Step 2: Update `system_images.go`**

Same pattern:

```go
func listSystemImages(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	pool, err := container.NewRuntimePool(container.RuntimeOptions{Sandbox: false})
	if err != nil {
		return fmt.Errorf("initializing runtime: %w", err)
	}
	defer pool.Close()

	type runtimeImage struct {
		image  container.ImageInfo
		rtType container.RuntimeType
	}
	var all []runtimeImage
	pool.ForEachAvailable(func(rt container.Runtime) error {
		imgs, err := rt.ListImages(ctx)
		if err != nil {
			ui.Warnf("Failed to list %s images: %v", rt.Type(), err)
			return nil
		}
		for _, img := range imgs {
			all = append(all, runtimeImage{image: img, rtType: rt.Type()})
		}
		return nil
	})

	if jsonOut {
		images := make([]container.ImageInfo, len(all))
		for i, ri := range all {
			images[i] = ri.image
		}
		return json.NewEncoder(os.Stdout).Encode(images)
	}

	if len(all) == 0 {
		fmt.Println("No moat images found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "IMAGE ID\tTAG\tRUNTIME\tSIZE\tCREATED")
	for _, ri := range all {
		img := ri.image
		id := img.ID
		if len(id) > 12 {
			id = id[:12]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d MB\t%s\n",
			id, img.Tag, ri.rtType, img.Size/(1024*1024), formatAge(img.Created))
	}
	w.Flush()

	fmt.Println()
	fmt.Println("To remove an image:")
	fmt.Println("  docker rmi <image-id>           (Docker)")
	fmt.Println("  container image rm <image-id>   (Apple)")

	return nil
}
```

Add `"github.com/majorcontext/moat/internal/ui"` to imports.

- [ ] **Step 3: Verify compilation**

Run: `go build ./cmd/moat/ && go vet ./cmd/moat/...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add cmd/moat/cli/system_containers.go cmd/moat/cli/system_images.go
git commit -m "feat(system): show containers and images from all available runtimes

moat system containers and moat system images now query both Docker and
Apple runtimes. Output includes a RUNTIME column to identify which
runtime owns each resource."
```

---

### Task 7: Add RUNTIME column to `moat list`

Now that runs track their runtime, display it in the list output so users can see which runtime each run uses.

**Files:**
- Modify: `cmd/moat/cli/list.go`

- [ ] **Step 1: Add RUNTIME column**

In `listRuns()`, add a RUNTIME column to the table headers and rows. Update the header (line 67-69):

```go
// Before:
if hasWorktree {
    fmt.Fprintln(w, "NAME\tRUN ID\tSTATE\tAGE\tWORKTREE\tENDPOINTS")
} else {
    fmt.Fprintln(w, "NAME\tRUN ID\tSTATE\tAGE\tENDPOINTS")
}

// After:
if hasWorktree {
    fmt.Fprintln(w, "NAME\tRUN ID\tRUNTIME\tSTATE\tAGE\tWORKTREE\tENDPOINTS")
} else {
    fmt.Fprintln(w, "NAME\tRUN ID\tRUNTIME\tSTATE\tAGE\tENDPOINTS")
}
```

Update the row output (lines 81-102) to include `r.Runtime`:

```go
rtLabel := r.Runtime
if rtLabel == "" {
    rtLabel = "-"
}
if hasWorktree {
    // ...
    fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
        r.Name, r.ID, rtLabel, r.GetState(), formatAge(r.CreatedAt), wt, endpoints)
} else {
    fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
        r.Name, r.ID, rtLabel, r.GetState(), formatAge(r.CreatedAt), endpoints)
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./cmd/moat/ && go vet ./cmd/moat/...`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add cmd/moat/cli/list.go
git commit -m "feat(list): show RUNTIME column in moat list output

Displays which container runtime (docker/apple) each run uses,
making it easy to identify cross-runtime runs."
```

---

### Task 8: Final lint, test, and cleanup

Run the full test suite and lint to catch any issues from the refactor.

**Files:**
- Possibly modify any files with lint issues

- [ ] **Step 1: Run lint**

Run: `make lint`
Expected: PASS (fix any issues)

- [ ] **Step 2: Run full unit test suite**

Run: `make test-unit`
Expected: PASS

- [ ] **Step 3: Run go vet**

Run: `go vet ./...`
Expected: PASS

- [ ] **Step 4: Check for any remaining `m.runtime.` references**

Run: `grep -rn 'm\.runtime\.' internal/run/manager.go`
Expected: No results (all replaced with `m.runtimePool` or `rt.`)

- [ ] **Step 5: Check for any remaining `container.NewRuntimeWithOptions` in CLI commands**

Run: `grep -rn 'container\.NewRuntimeWithOptions\|container\.NewRuntime()' cmd/moat/cli/`

Expected: Only `doctor.go` and `exec.go` (which sets `MOAT_RUNTIME` env before Manager creation — this is correct behavior since the runtime flag should influence the default runtime).

The `doctor.go` usage is a diagnostic tool that checks runtime availability — it intentionally probes a single runtime. This is fine as-is.

- [ ] **Step 6: Final commit (if any lint fixes)**

```bash
git add -A
git commit -m "fix: lint and vet fixes from multi-runtime refactor"
```

---

## Summary of changes

| Component | Before | After |
|-----------|--------|-------|
| `Manager.runtime` | Single `container.Runtime` | `*container.RuntimePool` with lazy init |
| Cross-runtime ops | Silently fail or skip | Resolve correct runtime via `runtimeForRun()` |
| `moat stop/destroy` | Wrong runtime → error | Correct runtime for each run |
| `moat exec` | Wrong runtime → error | Correct runtime for each run |
| `moat status` | Shows one runtime's images/containers | Merges all available runtimes |
| `moat clean` | Cleans one runtime's resources | Cleans all available runtimes |
| `moat system images/containers` | Lists one runtime | Lists all runtimes with RUNTIME column |
| `moat list` | No runtime info | Shows RUNTIME column |
| `loadPersistedRuns` | Skips cross-runtime checks | Uses pool to check with correct runtime |
| `monitorContainerExit` | Skipped for cross-runtime runs | Runs for all runs via correct runtime |
