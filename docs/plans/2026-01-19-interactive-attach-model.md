# Interactive and Attach Model for Moat

**Date:** 2026-01-19
**Status:** Implemented

## Problem Statement

Currently, `moat run -- bash` hangs because:
1. Stdin is not forwarded to the container
2. TTY is configured but not actually attached
3. There's no way to detach from a running container without killing it

## First Principles

### What is a "run"?

A run is an isolated execution environment with a lifecycle independent of the terminal that started it. This is already reflected in the design:
- Runs have IDs and can be listed (`moat list`)
- Runs can be stopped (`moat stop`)
- Logs persist and can be viewed later (`moat logs`)
- Runs survive and can be inspected after completion

### What does the user want?

1. **Start something and watch it** - Most common. Run a command, see output, wait for completion.
2. **Start something and walk away** - Background processing. Start a long task, detach, come back later.
3. **Interact with something** - Shell access, REPL, debugging. Requires bidirectional I/O.

### Key Insight

These aren't different modes of *running* - they're different modes of *attaching*.

## North Star

The run itself should always be independent. What changes is how (or whether) your terminal is attached to it.

```bash
# Start and attach (default) - you see output, ctrl+c detaches (doesn't kill)
moat run ./my-project

# Start detached - returns immediately, run continues in background
moat run --detach ./my-project

# Attach to existing run - reconnect to see output or interact
moat attach <run-id>

# Interactive - allocate PTY, forward stdin
moat run -it -- bash
moat attach -it <run-id>
```

This mirrors Docker's model because it's fundamentally correct:
- `docker run` vs `docker run -d` vs `docker attach`
- The container exists independently; attachment is separate

### Key Behaviors

- **Ctrl+C while attached** = detach, not kill (run continues)
- **Ctrl+C twice or `moat stop`** = actually stop the run
- **Non-interactive commands** work fine when attached (you just don't type anything)
- **Interactive commands** need `-it` to allocate PTY and forward stdin

## Why "Always Interactive" Doesn't Work

If stdin is always forwarded:
- Piping doesn't work well: `echo "input" | moat run -- cat` would compete with terminal stdin
- Background-ability is confusing - is stdin still connected?
- Resource overhead of maintaining bidirectional streams for commands that don't need it

The distinction between "I want to see output" vs "I want to interact" is meaningful.

## Implementation Phases

### Phase 1: Fix Detach Semantics

**Goal:** Ctrl+C should detach, not kill

**Changes:**
- Modify signal handling in `cmd/moat/cli/run.go`
- Ctrl+C detaches from output stream but leaves container running
- Clear message: "Detached. Run still active. Use 'moat stop <id>' to terminate."
- Double Ctrl+C (or within short window) actually stops

**Scope:** ~50-100 lines, contained to CLI layer

### Phase 2: Add Explicit Detach Flag

**Goal:** `moat run --detach` starts without attaching

**Changes:**
- Add `--detach` / `-d` flag to run command
- Skip log streaming, return immediately with run ID
- User can `moat logs -f <id>` to follow output

**Scope:** ~20-30 lines in CLI, minimal

### Phase 3: Add Attach Command

**Goal:** `moat attach <run-id>` reconnects to running container

**Changes:**
- New `attach.go` command file
- Implement output streaming (similar to current log streaming)
- Ctrl+C detaches

**Scope:** New command, ~100-150 lines

### Phase 4: Interactive Mode

**Goal:** `moat run -it` and `moat attach -it` for interactive sessions

**Changes:**
- Add `-i` (stdin) and `-t` (tty) flags
- Extend `Runtime` interface with `Attach(ctx, id, AttachOptions)` method
- Docker: Use `ContainerAttach` API with hijacked connection
- Apple containers: Run without `--detach`, add `--interactive --tty` flags
- Terminal handling: raw mode, SIGWINCH for resize
- Bidirectional I/O copying

**Scope:** Significant - touches runtime interface, both implementations, CLI

### Phase 5: Polish

**Goal:** Better UX and guard rails

**Changes:**
- `moat run` with no command and no `-it` warns or suggests adding a command
- Smart detection: if command is `bash`, `sh`, `python` (no args), suggest `-it`
- Better error messages for common mistakes

**Scope:** Small UX improvements

## Technical Details

### Runtime Interface Extension (Phase 4)

```go
type AttachOptions struct {
    Stdin  io.Reader
    Stdout io.Writer
    Stderr io.Writer
    TTY    bool
}

type Runtime interface {
    // ... existing methods ...

    // Attach connects stdin/stdout/stderr to a running container
    Attach(ctx context.Context, id string, opts AttachOptions) error
}
```

### Docker Implementation (Phase 4)

```go
func (r *DockerRuntime) Attach(ctx context.Context, id string, opts AttachOptions) error {
    resp, err := r.client.ContainerAttach(ctx, id, container.AttachOptions{
        Stream: true,
        Stdin:  opts.Stdin != nil,
        Stdout: true,
        Stderr: true,
    })
    if err != nil {
        return err
    }
    defer resp.Close()

    // Bidirectional copy with proper cleanup
    // Handle TTY vs non-TTY multiplexing
}
```

### Apple Container Implementation (Phase 4)

For interactive mode, instead of:
```go
args := []string{"run", "--detach", ...}
```

Use:
```go
if opts.Interactive {
    args := []string{"run", "--interactive", "--tty", ...}
    // Run in foreground, connect stdin/stdout directly
}
```

## Open Questions (Resolved)

1. **Should `moat logs -f` also support attaching stdin?** - No, `moat logs` remains read-only. Use `moat attach -it` for interactive sessions.
2. **How to handle the case where user runs `moat run -it` but the command doesn't need a TTY?** - The TTY is allocated anyway; no harm done.
3. **Should we auto-detect TTY need based on whether stdin is a terminal?** - We warn users when they run common interactive commands (`bash`, `sh`, `python`) without `-it` and suggest adding the flag.

## Success Criteria

All criteria met:

- ✅ `moat run -- npm start` shows output, Ctrl+C detaches, run continues
- ✅ `moat run -d -- npm start` returns immediately
- ✅ `moat attach <id>` reconnects to see output
- ✅ `moat run -it -- bash` gives interactive shell
- ✅ `moat attach -it <id>` gives interactive access to running container

## Additional Features Implemented

Beyond the original plan:

- **Run persistence**: Runs survive manager restart. On startup, manager reconciles run state with actual container state via `ContainerState()`.
- **Recent logs on attach**: When attaching to an interactive session, the last 50 lines of logs are displayed to provide context.
- **Output-only attach uses logs**: `moat attach` (without `-it`) uses container logs with follow mode for more reliable streaming.
- **Double Ctrl+C window**: 500ms window for double-press detection is tuned for usability.
- **Container exit detection delay**: 200ms delay before checking run state allows container exit to be detected reliably.
- **Interactive mode in agent.yaml**: Can set `interactive: true` in config to default to interactive mode.
