# Interactive-Only Simplification

> **Status:** Implemented. Merged via PR #196 on the `fix/interactive-only` branch.

**Goal:** Simplify moat's execution model to two clean modes: interactive (always attached, no detach) and non-interactive (blocking with log streaming to the terminal).

**Architecture:** Remove the `moat attach` command, the `--detach` flag, escape proxy detach sequences, and all "output-only attached" code paths. Interactive runs own the terminal for their lifetime. Non-interactive runs block while streaming container output, with Ctrl+C for graceful shutdown.

**Tech Stack:** Go, Cobra CLI, existing container runtime interface

---

## Context

The current model has accumulated complexity around attach/detach:

- 4 execution functions: `ExecuteRun`, `RunAttached`, `RunInteractive`, `RunInteractiveAttached`
- Separate `moat attach` command with its own interactive/non-interactive modes
- Escape proxy with Ctrl-/ d (detach) and Ctrl-/ k (stop)
- Double Ctrl+C detection for "detach vs stop" in non-interactive attached mode
- `--detach` flag that makes non-interactive runs print advice about `moat attach`

The new model:

| Mode | Behavior | Terminal | Exit |
|------|----------|----------|------|
| **Interactive** (`-i`) | Stdin/stdout/stderr connected, TTY allocated | Owns terminal | Ctrl-/ k to stop. Ctrl+C goes to container. Process exit ends session. |
| **Non-interactive** (default) | Output streamed to terminal. Blocks until exit. | Stdout only | Ctrl+C to stop. Process exit ends session. |

## What Gets Removed

- `cmd/moat/cli/attach.go` — entire file deleted
- `RunAttached()` in `exec.go` — non-interactive attached mode
- `RunInteractive()` in `exec.go` — old interactive path (superseded by `RunInteractiveAttached`)
- `--detach` / `-d` flag from `ExecFlags`
- `EscapeDetach` action from escape proxy (keep `EscapeStop`)
- Double Ctrl+C detection logic
- All "Use 'moat attach ...'" messages
- `attachOutputMode()` and `attachInteractiveMode()` from attach.go

## What Changes

- `ExecuteRun()` simplified: interactive → `RunInteractiveAttached()`, non-interactive → start + stream logs + wait for exit
- Escape proxy simplified: only Ctrl-/ k (stop) remains. Ctrl-/ d removed.
- `EscapeHelpText()` updated to only show stop sequence
- `moat wt` defaults to non-interactive (remove the `interactive := !wtFlags.Detach` hack)
- Provider CLIs (claude, codex, gemini): remove references to `moat attach` in messages
- Help text for `moat run` updated to remove detach/attach documentation

## What Stays

- `RunInteractiveAttached()` — this is the only attached execution path
- `StartAttached()` in container runtime — still needed for interactive
- `Attach()` in container runtime — can be removed from the interface (only used by attach command)
- `FollowLogs()` / `RecentLogs()` in manager — still used by `moat logs`
- Status bar / TUI — still used for interactive sessions
- TTY tracing — still used for interactive sessions
- `EscapeStop` action — Ctrl-/ k still stops the run

---

### Task 1: Remove `moat attach` command

**Files:**
- Delete: `cmd/moat/cli/attach.go`

**Step 1: Delete the attach command file**

Delete `cmd/moat/cli/attach.go` entirely. This removes:
- `attachCmd` cobra command and its `init()` registration
- `attachToRun()`, `attachOutputMode()`, `attachInteractiveMode()`
- `attachInteractive` and `attachTTYTrace` variables
- Timing constants (`doublePressWindow`, `containerExitCheckDelay`, `ttyStartupDelay`)

**Step 2: Verify it compiles**

Run: `go build ./...`
Expected: BUILD SUCCESS (no other files import from attach.go — the constants and functions are file-local)

Note: `ttyStartupDelay` is also used in `exec.go`. If the build fails because of this, move that constant to `exec.go` before deleting.

**Step 3: Commit**

```bash
git add -A
git commit -m "refactor(cli): remove moat attach command

Simplifying to two modes: interactive (attached) and non-interactive
(monitor via moat logs). The attach command allowed reconnecting to
running containers, which added complexity without sufficient value."
```

---

### Task 2: Remove `--detach` flag and simplify `ExecFlags`

**Files:**
- Modify: `internal/cli/types.go`
- Modify: `cmd/moat/cli/run.go`
- Modify: `cmd/moat/cli/exec.go`
- Modify: `cmd/moat/cli/wt.go`
- Modify: `internal/providers/claude/cli.go`
- Modify: `internal/providers/codex/cli.go`
- Modify: `internal/providers/gemini/cli.go`

**Step 1: Remove `Detach` from `ExecFlags`**

In `internal/cli/types.go`, remove the `Detach bool` field from `ExecFlags` struct and the corresponding `cmd.Flags().BoolVarP(&flags.Detach, "detach", "d", ...)` line from `AddExecFlags`.

**Step 2: Remove `TTYTrace` from `ExecFlags`**

The `TTYTrace` field on `ExecFlags` was only used by the attach command's `--tty-trace` flag. Remove it. (Note: `RunInteractiveAttached` takes `tracePath` as a parameter, and the `moat run` command doesn't expose this flag — it was attach-only.)

Actually, check if `moat run` passes `opts.Flags.TTYTrace` anywhere. If it does, keep the field. If not, remove it.

**Step 3: Update `ExecuteRun` in `exec.go`**

Remove the `opts.Flags.Detach` check and the detached-mode message block. The function should now:

1. If interactive: call `RunInteractiveAttached()` (same as now)
2. If non-interactive: `manager.Start()` → stream logs via `FollowLogs` → block on `manager.Wait()` with signal handling

Replace the current detach block and `RunAttached` call with a blocking loop that streams container output to stdout while waiting for exit or Ctrl+C. See `cmd/moat/cli/exec.go` for the final implementation.

**Step 4: Remove `RunAttached` and `RunInteractive` from `exec.go`**

Delete the `RunAttached()` function entirely (non-interactive attached mode).
Delete the `RunInteractive()` function entirely (old interactive path, superseded by `RunInteractiveAttached`).

**Step 5: Update `moat run` help text**

In `cmd/moat/cli/run.go`, update the `Long` description to remove references to detach, attach. The new help should describe:

```
Non-interactive mode (default):
  Output streams to the terminal. Press Ctrl+C to stop.

Interactive mode (-i):
  Ctrl-/ k          Stop the run
  Ctrl+C            Sent to container process
```

Remove `-d` from examples.

**Step 6: Fix `moat wt` interactive logic**

In `cmd/moat/cli/wt.go`, the current code does `interactive := !wtFlags.Detach` which made worktree runs interactive by default (unless detached). With the removal of detach, change this to respect the config:

```go
// Determine interactive mode: CLI flags > config > default (non-interactive)
interactive := false
if cfg != nil && cfg.Interactive {
    interactive = true
}
```

Note: `moat wt` doesn't have its own `-i` flag. If the moat.yaml says `interactive: true`, it'll be interactive. Otherwise non-interactive. This is the right default for worktree runs.

**Step 7: Update provider CLIs**

In `internal/providers/claude/cli.go`, `internal/providers/codex/cli.go`, and `internal/providers/gemini/cli.go`:
- Remove references to `Detach` flag (e.g., `!claudeFlags.Detach` checks)
- Remove any "Use moat attach" messages
- The providers already determine interactivity correctly (prompt flag = non-interactive, no prompt = interactive)

**Step 8: Verify build**

Run: `go build ./...`

**Step 9: Run tests**

Run: `make test-unit`

**Step 10: Commit**

```bash
git add -A
git commit -m "refactor(cli): remove --detach flag and simplify execution modes

Non-interactive runs now always start in background. Interactive runs
always own the terminal. Removes RunAttached and RunInteractive in
favor of the single RunInteractiveAttached path."
```

---

### Task 3: Simplify escape proxy (remove detach action)

**Files:**
- Modify: `internal/term/escape.go`
- Modify: `internal/term/escape_test.go`

**Step 1: Remove `EscapeDetach` from escape proxy**

In `internal/term/escape.go`:

1. Remove `EscapeDetach` from the `EscapeAction` constants (keep `EscapeNone` and `EscapeStop`)
2. Remove the `case EscapeDetach:` from `EscapeError.Error()`
3. Remove `escapeKeyDetach` constant (`byte = 'd'`)
4. In `EscapeProxy.Read()`, remove the `case escapeKeyDetach:` handling — when 'd' follows Ctrl-/, treat it as an unrecognized key (pass both bytes through)

**Step 2: Update `EscapeHelpText()`**

Change to: `"Escape: Ctrl-/ k (stop)"`

**Step 3: Update tests**

In `internal/term/escape_test.go`, remove or update tests for the detach escape sequence. Tests for:
- Ctrl-/ d should now pass both bytes through (not trigger escape)
- Ctrl-/ k should still trigger `EscapeStop`
- Ctrl-/ Ctrl-/ should still pass single Ctrl-/ through

**Step 4: Run tests**

Run: `go test ./internal/term/ -v`

**Step 5: Commit**

```bash
git add -A
git commit -m "refactor(term): remove detach escape sequence

Only Ctrl-/ k (stop) remains. Ctrl-/ d is no longer an escape
sequence and passes through to the container."
```

---

### Task 4: Clean up `RunInteractiveAttached` (remove detach handling)

**Files:**
- Modify: `cmd/moat/cli/exec.go`

**Step 1: Remove detach handling from `RunInteractiveAttached`**

In `RunInteractiveAttached()`:

1. Remove the `case term.EscapeDetach:` from the escape action switch. Only `EscapeStop` remains.
2. In the `case err := <-attachDone:` handler, remove the "detached" message path. If attachment ends unexpectedly (container still running), treat it as an error or a clean exit, not a detach.

The simplified `attachDone` handler:

```go
case err := <-attachDone:
    if err != nil && ctx.Err() == nil && !term.IsEscapeError(err) {
        log.Error("run failed", "id", r.ID, "error", err)
        return fmt.Errorf("run failed: %w", err)
    }
    fmt.Printf("Run %s completed\n", r.ID)
    return nil
```

**Step 2: Update escape help text in the function**

The `fmt.Printf("%s\n\n", term.EscapeHelpText())` call will now print the updated text.

**Step 3: Remove detach-related messages**

Remove all `"Use 'moat attach %s' to reattach"` messages from exec.go.

**Step 4: Verify build and run tests**

Run: `go build ./... && make test-unit`

**Step 5: Commit**

```bash
git add -A
git commit -m "refactor(cli): remove detach handling from interactive mode

Interactive sessions now run until the process exits or user sends
Ctrl-/ k to stop. No detach path exists."
```

---

### Task 5: Remove `Attach()` from container runtime interface

**Files:**
- Modify: `internal/container/runtime.go`
- Modify: `internal/container/docker.go`
- Modify: `internal/container/apple.go`
- Modify: `internal/run/manager.go`

**Step 1: Check if `Attach()` is still used**

Search for calls to `runtime.Attach(` and `manager.Attach(`. After removing `attach.go` and `RunInteractive()`, the only remaining call should be in `manager.Attach()`. If nothing calls `manager.Attach()` anymore, we can remove it from both the manager and the runtime interface.

**Step 2: Remove `Attach` from runtime interface**

In `internal/container/runtime.go`, remove:
```go
Attach(ctx context.Context, id string, opts AttachOptions) error
```

**Step 3: Remove `Attach` implementations**

- In `internal/container/docker.go`, remove the `Attach()` method
- In `internal/container/apple.go`, remove the `Attach()` method

**Step 4: Remove `Manager.Attach()` from run manager**

In `internal/run/manager.go`, remove the `Attach()` method.

**Step 5: Verify build**

Run: `go build ./...`

**Step 6: Run tests**

Run: `make test-unit`

**Step 7: Commit**

```bash
git add -A
git commit -m "refactor(container): remove Attach from runtime interface

Only StartAttached remains for interactive sessions. The separate
Attach method was only needed for the removed moat attach command."
```

---

### Task 6: Clean up references and messages

**Files:**
- Modify: `internal/providers/claude/cli.go`
- Modify: `internal/providers/claude/cli_test.go`
- Modify: `cmd/moat/cli/wt.go`
- Modify: `internal/cli/worktree.go`

**Step 1: Update error messages that reference `moat attach`**

Search for all remaining "moat attach" references:

1. `internal/providers/claude/cli.go:424` — Change from "Use 'moat attach %s' to reconnect" to "Use 'moat logs %s' to view output or 'moat stop %s' to stop"
2. `internal/providers/claude/cli_test.go:356-357` — Update test to check for new message
3. `cmd/moat/cli/wt.go:146` — Change from "Attach with 'moat attach'" to "View logs with 'moat logs %s' or stop with 'moat stop %s'"
4. `internal/cli/worktree.go:67` — Same pattern as above

**Step 2: Update help text**

Review `moat run --help` and `moat wt --help` to ensure no attach/detach references remain.

**Step 3: Verify build and tests**

Run: `go build ./... && make test-unit`

**Step 4: Commit**

```bash
git add -A
git commit -m "fix(cli): update messages to remove moat attach references

Point users to moat logs and moat stop instead of the removed
moat attach command."
```

---

### Task 7: Update documentation

**Files:**
- Modify: `docs/content/reference/01-cli.md` (if it exists)
- Modify: `docs/content/reference/02-moat-yaml.md` (if `interactive` field docs need updating)
- Modify: `docs/plans/2026-01-19-interactive-attach-model.md` (mark as superseded)

**Step 1: Update CLI reference docs**

Remove documentation for:
- `moat attach` command
- `--detach` / `-d` flag
- Ctrl-/ d escape sequence

Update documentation for:
- `moat run` — describe the two modes (interactive and non-interactive)
- Escape sequences — only Ctrl-/ k

**Step 2: Update moat.yaml reference**

If the `interactive` field is documented, update to clarify its new meaning:
- `interactive: true` — allocates TTY, connects stdin, session owns terminal
- `interactive: false` (default) — runs in background, monitor via `moat logs`

**Step 3: Mark old plan as superseded**

Add a note at the top of `docs/plans/2026-01-19-interactive-attach-model.md`:

```markdown
> **Status:** Superseded by [Interactive-Only Simplification](2026-02-27-interactive-only-simplification.md)
```

**Step 4: Commit**

```bash
git add -A
git commit -m "docs: update for interactive-only execution model

Remove documentation for moat attach command, --detach flag, and
Ctrl-/ d escape sequence. Document the simplified two-mode model."
```

---

### Task 8: Run full test suite and lint

**Step 1: Run linter**

Run: `make lint`

Fix any issues.

**Step 2: Run full unit tests**

Run: `make test-unit`

Fix any failures.

**Step 3: Commit any fixes**

```bash
git add -A
git commit -m "fix: address lint and test issues from simplification"
```

---

## Implementation Notes

The following additional changes were made during implementation beyond the original plan:

### Non-interactive mode: blocking with log streaming (not fire-and-forget)

The original plan called for fire-and-forget non-interactive runs. During implementation, this was found to be problematic because `manager.Close()` cancels the monitor goroutine's context, killing log capture before it completes. The solution: non-interactive runs block while streaming container output to stdout (similar to `docker run`). This ensures the monitor goroutine finishes before the process exits. True fire-and-forget requires daemon-level monitoring (tracked in issue #197).

### Monitor goroutine lifecycle fix

`monitorContainerExit` was changed to use `context.Background()` instead of the manager's context, so `Close()` doesn't kill it. A `sync.WaitGroup` in `Close()` ensures the runtime stays alive while monitors finish. Only monitors started via `Start()` are tracked — inherited monitors from `loadPersistedRuns` are fire-and-forget since they belong to other CLI invocations.

### Docker log demuxing

`ContainerLogs` for non-TTY containers returns Docker's multiplexed stdout/stderr format. Added demuxing via `stdcopy.StdCopy` with `io.Pipe` to produce clean output. Also fixed inspect failure to return an error instead of silently producing garbled output.

### Interactive resource cleanup

`cleanupResources` was not called on natural interactive exit (only on escape-stop and signal paths). Added cleanup in the `StartAttached` non-error path to prevent resource leaks.

### Interactive metadata updates

After an interactive container exits normally, `state` and `stopped_at` are now updated via state transition in `StartAttached`.
