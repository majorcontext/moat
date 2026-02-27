# tmux-Based Interactive Sessions

## Problem

Detaching from interactive containers kills the user's process. Root cause: canceling the `container exec` kills the exec'd process. The container survives (sleep infinity PID 1), but on reattach you get a fresh shell — any in-progress work is lost. "Detach/reattach" is effectively "kill/restart."

Additionally, on reattach the screen doesn't repaint (user sees a blank terminal with partial updates), and scrollback from the previous session is lost.

## Solution

Use tmux as a session manager inside the container. tmux owns the user's process lifetime and terminal state. The CLI is just a client that connects and disconnects.

```
Container PID 1:  sleep infinity
Interactive run:  container exec → tmux new-session -s moat -- <user command>
Detach:           kill exec → tmux client disconnects, server + process continue
Reattach:         container exec → tmux attach-session -t moat
```

## Architecture

### Two-layer model

**Layer 1: Exec-based container lifecycle** (from stashed work on feat/proxy-daemon)

Container runs `sleep infinity` as PID 1 via `MOAT_EXEC_MODE=1`. User's command runs via `container exec`. Killing the exec leaves the container alive. Runtime interface: `Exec()` + `ResizeExec()` replace `Attach()` + `StartAttached()` + `ResizeTTY()`. Unified `RunInteractive()` for both initial run and reattach.

**Layer 2: tmux session management** (new work)

tmux installed in all moat-built images. Manager wraps the exec command with tmux. On detach, tmux keeps the process alive. On reattach, tmux reconnects to the same session with full screen redraw and scrollback.

### Flows

**Start:** `moat run -i -- claude`
1. Create container with `MOAT_EXEC_MODE=1`
2. Start container (PID 1 = sleep infinity)
3. Wait for readiness sentinel (`/tmp/.moat-ready`)
4. `Exec()` wraps command: `tmux -f /tmp/.moat-tmux.conf new-session -s moat -x <cols> -y <rows> -- claude`
5. User interacts with claude through tmux

**Detach (Ctrl-/ d):**
1. moat's escape proxy catches the sequence
2. Exec context canceled → container exec killed
3. tmux sees client disconnect → server keeps running with user's process
4. CLI prints "Detached" and exits

**Reattach:** `moat attach <run-id>`
1. Check `tmux has-session -t moat` in container
2. If exists: exec `tmux attach-session -t moat` — full screen redraw
3. If not: "Run finished while detached" + recent logs → stop container

**User exits normally:**
1. User types `exit` → bash exits → tmux session closes → exec finishes
2. CLI checks `has-session` → session gone
3. `manager.Stop()` kills the keepalive container
4. "Run completed"

**tmux's own detach (Ctrl-b d):**
1. tmux detaches client → exec finishes normally
2. CLI checks `has-session` → still exists → "Detached"

**Resize:**
1. SIGWINCH → moat status bar adjusts scroll region
2. `ResizeExec()` resizes exec PTY
3. tmux detects size change → re-renders

### tmux configuration

Written by moat-init.sh at `/tmp/.moat-tmux.conf`:

```
set -g history-limit 100000
set -g status off
set -g escape-time 0
set -g mouse on
```

- `status off` — moat has its own status bar; hide tmux's
- `escape-time 0` — no delay on Escape key (important for vim)
- `history-limit 100000` — generous scrollback
- `mouse on` — scroll support

### Command construction

New session:
```
["tmux", "-f", "/tmp/.moat-tmux.conf", "new-session", "-s", "moat", "-x", "<cols>", "-y", "<rows>", "--", <user-cmd...>]
```

Reattach:
```
["tmux", "attach-session", "-t", "moat"]
```

Session check:
```
["tmux", "has-session", "-t", "moat"]
```

### Image changes

Add `tmux` to `baseAptPackages` in dockerfile.go. ~1-2MB overhead, negligible vs base image size. Ensures tmux is always available in moat-built images.

For bare images without moat-init (no grants), tmux won't be present. Those fall back to raw exec — detach kills process, no reattach. This is acceptable since the primary use case (Claude Code, Codex) always uses moat-built images.

### Log capture

Output is tee'd to logBuffer while attached (same as exec-based model). Output produced while detached goes to tmux's scrollback but not to logs.jsonl. User can scroll up in tmux on reattach. Full capture via `pipe-pane` can be added later if needed.

### Non-interactive runs

Completely unchanged. No tmux, no exec model. User's command is PID 1.

## Files changed

### From stash (exec-based model)

| File | Change |
|------|--------|
| `internal/container/runtime.go` | Replace `Attach`/`StartAttached`/`ResizeTTY` with `Exec`/`ResizeExec`; `ExecOptions` replaces `AttachOptions` |
| `internal/container/apple.go` | Implement `Exec` (PTY + pipes), `ResizeExec`; delete `Attach`, `StartAttached`, `startAttachedWithPTY/Pipes`, `killAttachCLI` |
| `internal/container/docker.go` | Implement `Exec` (Docker exec API), `ResizeExec`; delete `Attach`, `StartAttached` |
| `internal/run/manager.go` | Replace `StartAttached`/`Attach`/`ResizeTTY` with `Exec`/`ResizeExec`/`waitForReady`; modify `Create` and `Start` for exec mode; skip cleanup for running containers in `Close` |
| `internal/run/run.go` | Add `ExecCmd` field |
| `internal/storage/storage.go` | Add `ExecCmd` to `Metadata` |
| `internal/deps/scripts/moat-init.sh` | Add `MOAT_EXEC_MODE` check before exec block |
| `cmd/moat/cli/exec.go` | Replace `RunInteractiveAttached` with `RunInteractive`; simplify `ExecuteRun` |
| `cmd/moat/cli/attach.go` | Delete `attachInteractiveMode`, use `RunInteractive` |

### New (tmux layer)

| File | Change |
|------|--------|
| `internal/deps/dockerfile.go` | Add `tmux` to `baseAptPackages` |
| `internal/deps/scripts/moat-init.sh` | Write tmux config file in exec mode |
| `internal/run/manager.go` | `Exec()` wraps cmd with tmux (new-session or attach-session); add `hasTmuxSession()` helper |
| `cmd/moat/cli/exec.go` | `RunInteractive()` — after exec finishes, check tmux session to decide "detached" vs "completed"; handle "finished while detached" on reattach |
| `cmd/moat/cli/attach.go` | Show "finished while detached" + recent logs when no tmux session |
