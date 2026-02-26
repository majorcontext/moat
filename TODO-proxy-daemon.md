# Proxy Daemon Branch — Known Issues

Issues discovered during investigation of orphaned process leak on 2026-02-25.
E2E tests on this branch spawned ~2,854 orphaned daemon processes, ~1,100 stuck
container CLI processes, and overwhelmed the Apple container apiserver via XPC.

## Critical: Daemon Process Leak

### Race condition in `daemon.EnsureRunning()` (`internal/daemon/lifecycle.go`)

`EnsureRunning` reads `daemon.lock`, and if no daemon is alive, spawns a new one.
The daemon writes the lock file AFTER it starts. Between read and write there is a
window where concurrent callers all see "no daemon" and all spawn new processes.

With `Setsid: true`, each spawned daemon is fully detached and survives parent exit.

**Fix:** Use `syscall.Flock` (or a separate `.lock` file with advisory locking) to
serialize the read-check-spawn sequence. Only one caller should be able to enter the
"start new daemon" path at a time.

### No process group cleanup on test exit

When e2e tests fail or time out, `os.StartProcess` daemons with `Setsid: true`
become orphaned. Test teardown (`defer mgr.Close()`) doesn't run on panics or
`t.FailNow()` from other goroutines.

**Fix options:**
- Track spawned daemon PIDs and kill them in `TestMain` cleanup
- Use a process group and `killpg` in a cleanup handler
- Have `EnsureRunning` return a cleanup function that kills the daemon

### `MOAT_EXECUTABLE` fallback spawns wrong binary

When `MOAT_EXECUTABLE` is unset, `EnsureRunning` falls back to `os.Executable()`.
In test binaries this returns the `e2e.test` binary, which doesn't have the
`_daemon` Cobra command — producing stuck processes that can't parse their args.

**Fix:** Make `EnsureRunning` fail explicitly if `MOAT_EXECUTABLE` is unset and
the resolved binary is a test binary (check `os.Args[0]` for `.test` suffix).

## Important: Missing Timeouts on Container CLI Operations

### `ContainerState` has no per-call timeout (`internal/container/apple.go:1076`)

`container inspect <id>` takes ~0.6s per call. `loadPersistedRuns` calls it for
every persisted run with `context.Background()` (no timeout). With hundreds of
stale runs, this blocks `moat status` and `moat list` for minutes.

This is a pre-existing issue on `main` too — not specific to the daemon branch.

### Other container CLI operations lack timeouts

`container create`, `container rm`, `container start`, `container logs` are all
called without context deadlines. When the Apple container apiserver is overloaded,
these block indefinitely.

## Moderate: Idle Timer Ineffective When Daemon Is Stuck

The 5-minute idle timer sends `SIGTERM` to self via `sigCh`. But if daemon
goroutines are blocked on container CLI calls (which have no timeout), the
shutdown sequence blocks on those operations. The daemon effectively hangs.

**Fix:** Ensure all container CLI calls in the daemon use contexts with deadlines.
The shutdown handler should use `context.WithTimeout` for cleanup operations.
