# tmux Interactive Sessions Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Enable interactive container sessions to detach and reattach without losing the running process or terminal state, using tmux as a session manager.

**Architecture:** Two layers — (1) exec-based container model where PID 1 is `sleep infinity` and user commands run via `container exec`, (2) tmux session management where `container exec` runs tmux, which owns the process lifetime. Detach kills the exec but tmux keeps the process alive. Reattach creates a new exec that reconnects to tmux.

**Tech Stack:** Go, tmux, Apple containers CLI, Docker API

**Design doc:** `docs/plans/2026-02-27-tmux-interactive-sessions-design.md`

---

## Phase 1: Apply exec-based model from stash

The stash `stash@{0}` on `feat/proxy-daemon` contains a tested exec-based implementation. Apply it first as the foundation.

### Task 1: Create branch and apply stash

**Step 1: Create feature branch from main**

```bash
git checkout main && git pull
git checkout -b feat/tmux-interactive-sessions
```

**Step 2: Apply the stash**

```bash
git stash pop 'stash@{0}'
```

**Step 3: Verify it compiles**

```bash
go build ./...
```

Expected: Success. If conflicts exist, resolve them — the stash was created on `feat/proxy-daemon` which has since been merged.

**Step 4: Run tests**

```bash
make test-unit
```

Expected: All pass. Fix any failures from merge drift.

**Step 5: Run lint**

```bash
make lint
```

Expected: Clean. Fix any issues.

**Step 6: Commit**

```bash
git add -A
git commit -m "feat(interactive): exec-based container model for detach/reattach

Replace StartAttached/Attach/ResizeTTY with Exec/ResizeExec across both
container runtimes. Interactive containers now run sleep infinity as PID 1
(via MOAT_EXEC_MODE=1 in moat-init.sh) and user commands execute via
container exec. This lets the container survive detach.

Unified RunInteractive() handles both initial run and reattach. ExecCmd
is persisted in run metadata for reattach.

Removes ~450 lines of duplicate interactive session code."
```

---

## Phase 2: Add tmux to container images

### Task 2: Add tmux to base apt packages

**Files:**
- Modify: `internal/deps/dockerfile.go:345`
- Modify: `internal/deps/dockerfile_test.go:110`

**Step 1: Add tmux to baseAptPackages**

In `internal/deps/dockerfile.go`, line 345, change:
```go
var baseAptPackages = []string{"ca-certificates", "curl", "gnupg", "gosu", "unzip"}
```
to:
```go
var baseAptPackages = []string{"ca-certificates", "curl", "gnupg", "gosu", "tmux", "unzip"}
```

Note: Keep alphabetical order. `tmux` goes between `gosu` and `unzip`.

**Step 2: Run tests**

```bash
go test ./internal/deps/ -run TestGenerateDockerfile -v
```

Expected: Pass. The existing test checks for `ca-certificates` but not for the full list. No test changes strictly needed, but add one for `tmux`:

In `internal/deps/dockerfile_test.go`, after line 112 (after the `ca-certificates` check), add:
```go
if !strings.Contains(result.Dockerfile, "tmux") {
    t.Error("Dockerfile should include base package tmux")
}
```

**Step 3: Run the updated test**

```bash
go test ./internal/deps/ -run TestGenerateDockerfile -v
```

Expected: Pass.

**Step 4: Commit**

```bash
git add internal/deps/dockerfile.go internal/deps/dockerfile_test.go
git commit -m "feat(deps): add tmux to base container packages

tmux is needed for interactive session management — it owns the process
lifetime so detach/reattach works without killing the running process.
~1-2MB overhead, negligible vs base image."
```

---

## Phase 3: Write tmux config in init script

### Task 3: Update moat-init.sh to write tmux config

**Files:**
- Modify: `internal/deps/scripts/moat-init.sh:307`

The stash already added a `MOAT_EXEC_MODE` block. Extend it to write the tmux config file.

**Step 1: Add tmux config to the MOAT_EXEC_MODE block**

In `internal/deps/scripts/moat-init.sh`, find the exec mode block (added by stash):
```bash
if [ "$MOAT_EXEC_MODE" = "1" ]; then
  touch /tmp/.moat-ready
  exec sleep infinity
fi
```

Replace with:
```bash
if [ "$MOAT_EXEC_MODE" = "1" ]; then
  # Write tmux config for interactive sessions.
  # status off: moat has its own status bar
  # escape-time 0: no delay on Escape key (vim users)
  # history-limit: generous scrollback for reattach
  # mouse on: scroll support
  cat > /tmp/.moat-tmux.conf << 'TMUXEOF'
set -g history-limit 100000
set -g status off
set -g escape-time 0
set -g mouse on
TMUXEOF
  touch /tmp/.moat-ready
  exec sleep infinity
fi
```

**Step 2: Verify build**

```bash
go build ./...
```

Expected: Pass (the script is embedded).

**Step 3: Commit**

```bash
git add internal/deps/scripts/moat-init.sh
git commit -m "feat(init): write tmux config in exec mode

Creates /tmp/.moat-tmux.conf with session settings: no status bar
(moat has its own), no escape delay, 100K scrollback, mouse support."
```

---

## Phase 4: Manager tmux integration

### Task 4: Add tmux session management to Manager.Exec

**Files:**
- Modify: `internal/run/manager.go` (the `Exec` method and new helpers)

The stash gives us a working `Exec()` method that does raw exec. We need to wrap the command with tmux.

**Step 1: Add hasTmuxSession helper**

Add this method to `internal/run/manager.go`, near the `Exec` method:

```go
// hasTmuxSession checks if a tmux session named "moat" exists in the container.
// Returns true if the session is running, false otherwise.
func (m *Manager) hasTmuxSession(ctx context.Context, containerID string) bool {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	exitCode, err := m.runtime.Exec(checkCtx, containerID, container.ExecOptions{
		Cmd: []string{"tmux", "has-session", "-t", "moat"},
	})
	return err == nil && exitCode == 0
}
```

**Step 2: Add tmux command builder**

Add this helper near `hasTmuxSession`:

```go
// buildTmuxNewSession wraps a command in a tmux new-session invocation.
func buildTmuxNewSession(cmd []string, width, height uint) []string {
	args := []string{"tmux", "-f", "/tmp/.moat-tmux.conf", "new-session", "-s", "moat"}
	if width > 0 && height > 0 {
		args = append(args, "-x", strconv.FormatUint(uint64(width), 10), "-y", strconv.FormatUint(uint64(height), 10))
	}
	args = append(args, "--")
	args = append(args, cmd...)
	return args
}
```

**Step 3: Modify Exec to wrap with tmux**

In the `Exec` method (from stash), after determining `useTTY`, `user`, and setting up `logBuffer`, but BEFORE building `execOpts`, add tmux wrapping logic:

```go
	// Wrap command with tmux for session management.
	// If a tmux session already exists (reattach), attach to it.
	// Otherwise, create a new session with the user's command.
	// If tmux isn't available (bare image without moat-init), fall back to raw exec.
	execCmd := cmd
	if useTTY {
		if m.hasTmuxSession(ctx, containerID) {
			execCmd = []string{"tmux", "attach-session", "-t", "moat"}
		} else {
			var w, h uint
			if term.IsTerminal(os.Stdout) {
				tw, th := term.GetSize(os.Stdout)
				if tw > 0 && th > 0 {
					w, h = uint(tw), uint(th)
				}
			}
			execCmd = buildTmuxNewSession(cmd, w, h)
		}
	}
```

Then use `execCmd` instead of `cmd` when building `execOpts`:
```go
	execOpts := container.ExecOptions{
		Cmd:    execCmd,  // was: cmd
		// ... rest unchanged
	}
```

Also add `"strconv"` to the import block if not already present.

**Step 4: Verify build**

```bash
go build ./...
```

Expected: Pass.

**Step 5: Commit**

```bash
git add internal/run/manager.go
git commit -m "feat(manager): wrap interactive exec with tmux sessions

Exec() now wraps user commands in tmux new-session for TTY mode.
On reattach, detects existing session and uses tmux attach-session.
Falls back to raw exec if tmux isn't available (bare images)."
```

---

## Phase 5: CLI tmux-aware session handling

### Task 5: Update RunInteractive to handle tmux lifecycle

**Files:**
- Modify: `cmd/moat/cli/exec.go` (the `RunInteractive` function from stash)

The stash's `RunInteractive` monitors exec completion and decides between "detached" and "completed." We need to add tmux session awareness.

**Step 1: Modify the exec-done handler in RunInteractive**

In `cmd/moat/cli/exec.go`, find the `RunInteractive` function's select loop. Find the case that handles exec completion (the `execDone` channel). The stash has logic that waits for container exit. Replace the exec-done handling with tmux-aware logic.

Find this pattern in RunInteractive (from stash):
```go
		case err := <-execDone:
			if err != nil && ctx.Err() == nil && !term.IsEscapeError(err) {
				log.Error("exec failed", "id", r.ID, "error", err)
			}
			// ...existing logic checking container state...
```

Replace the exec-done case body with:
```go
		case err := <-execDone:
			if err != nil && ctx.Err() == nil && !term.IsEscapeError(err) {
				log.Error("exec failed", "id", r.ID, "error", err)
			}

			// Check if tmux session is still running (user detached via
			// Ctrl-b d, or exec was killed but process continues in tmux).
			if manager.HasTmuxSession(ctx, r.ID) {
				fmt.Printf("\r\nDetached from run %s (still running)\r\n", r.ID)
				fmt.Printf("Use 'moat attach %s' to reattach\r\n", r.ID)
				return nil
			}

			// tmux session gone — user's process exited. Stop the keepalive container.
			if stopErr := manager.Stop(context.Background(), r.ID); stopErr != nil {
				log.Debug("failed to stop container after process exit", "error", stopErr)
			}
			fmt.Printf("Run %s completed\r\n", r.ID)
			return nil
```

**Step 2: Add HasTmuxSession to Manager (public wrapper)**

In `internal/run/manager.go`, add a public method that the CLI can call:

```go
// HasTmuxSession checks if a tmux session is still running for the given run.
func (m *Manager) HasTmuxSession(ctx context.Context, runID string) bool {
	m.mu.RLock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.RUnlock()
		return false
	}
	containerID := r.ContainerID
	m.mu.RUnlock()
	return m.hasTmuxSession(ctx, containerID)
}
```

**Step 3: Update attach.go for "finished while detached" case**

In `cmd/moat/cli/attach.go`, the stash replaces `attachInteractiveMode` with a call to `RunInteractive`. Before calling `RunInteractive`, add a check for the case where the process finished while the user was detached:

Find the interactive branch (from stash):
```go
if interactive {
    return RunInteractive(ctx, manager, r, r.ExecCmd, attachTTYTrace)
}
```

Replace with:
```go
if interactive {
    // Check if the process finished while we were detached.
    if r.ExecCmd != nil && !manager.HasTmuxSession(ctx, r.ID) {
        fmt.Printf("Run finished while detached.\n")
        if logs, err := manager.RecentLogs(r.ID, 20); err == nil && len(logs) > 0 {
            fmt.Printf("\nLast output:\n%s", logs)
            if logs[len(logs)-1] != '\n' {
                fmt.Println()
            }
        }
        fmt.Printf("\nStopping container...\n")
        if stopErr := manager.Stop(ctx, r.ID); stopErr != nil {
            log.Debug("failed to stop container", "error", stopErr)
        }
        return nil
    }
    return RunInteractive(ctx, manager, r, r.ExecCmd, attachTTYTrace)
}
```

**Step 4: Verify build**

```bash
go build ./...
```

Expected: Pass.

**Step 5: Run tests**

```bash
make test-unit
```

Expected: Pass.

**Step 6: Run lint**

```bash
make lint
```

Expected: Clean.

**Step 7: Commit**

```bash
git add cmd/moat/cli/exec.go cmd/moat/cli/attach.go internal/run/manager.go
git commit -m "feat(cli): tmux-aware session lifecycle in RunInteractive

After exec finishes, check if tmux session still exists to distinguish
'detached' (session alive) from 'completed' (session gone).

On moat attach, detect process-finished-while-detached case: show
last 20 lines of output and stop the container."
```

---

## Phase 6: Verification

### Task 6: Full verification pass

**Step 1: Build**

```bash
go build ./...
```

**Step 2: Unit tests with race detector**

```bash
make test-unit
```

**Step 3: Lint**

```bash
make lint
```

**Step 4: Review the full diff**

```bash
git diff main --stat
git diff main
```

Review for:
- No leftover `StartAttached`, `Attach`, `ResizeTTY` references (except in comments/docs)
- All `Exec`/`ResizeExec` properly wired
- tmux commands properly constructed
- No security issues (command injection via ExecCmd)

**Step 5: Manual testing (Apple containers)**

Build the binary:
```bash
go build -o moat ./cmd/moat
```

Test 1 — Interactive session with tmux:
```bash
./moat run -i --grant claude --workspace . -- bash
# Verify tmux is running: press Ctrl-b then type :list-sessions
# Should show session "moat"
# Type exit → "Run completed"
```

Test 2 — Detach and reattach:
```bash
./moat run -i --grant claude --workspace . -- bash
# Do some work, check scrollback
# Press Ctrl-/ d → "Detached from run ... (still running)"
./moat attach <run-id>
# Verify: same session, scrollback preserved, screen redrawn
# Type exit → "Run completed"
```

Test 3 — Process exits while detached:
```bash
./moat run -i --grant claude --workspace . -- bash -c 'echo hello && sleep 5'
# Press Ctrl-/ d to detach
# Wait 5+ seconds for the command to finish
./moat attach <run-id>
# Should show: "Run finished while detached" + last output
```

Test 4 — Non-interactive (unchanged):
```bash
./moat run --workspace . -- echo hello
# Should work as before, no tmux involvement
```

Test 5 — Stop while attached:
```bash
./moat run -i --grant claude --workspace . -- bash
# Press Ctrl-/ s → "Stopping run..." → clean exit
```
