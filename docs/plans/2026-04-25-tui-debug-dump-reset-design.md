# TUI Debug — On-Demand History Dump and Terminal Reset

## Problem

Interactive Moat sessions occasionally hit terminal-rendering bugs that are hard to reproduce: e.g. multiple Moat footers interlaced with the child's UI, after which the session is wedged. The existing `--tty-trace=FILE` flag captures everything from session start, but only when set up front — by the time the bug manifests, it's too late to start recording. There's also no way to recover the terminal short of killing the run.

This design adds two on-demand actions to the existing Ctrl+/ menu:

- **`d` (dump TUI history)** — writes the in-memory trace ring buffer to a file for offline analysis or feeding into a Claude session.
- **`r` (reset terminal)** — issues a hard terminal reset and nudges the child to redraw, attempting to unstick a wedged session.

## Goals

- Capture useful debug artifacts retroactively, with no flag required.
- Reuse the existing `Trace` JSON format and `moat tty-trace analyze` tool.
- Keep memory cost bounded and predictable.
- Recover from common stuck-footer cases without restarting the session.

## Non-goals

- Auto-detection of corruption or auto-reset. The user pulls the trigger.
- New analyzer features. Existing `moat tty-trace analyze` is sufficient.
- Persisted ring buffer across runs. Process-local memory only.
- Reproducing the underlying bug in tests. By definition we can't.

## Architecture

Three components, each in its existing package:

### 1. `internal/trace` — `RingRecorder`

New file `internal/trace/ring.go`. A `RingRecorder` records `Event`s into a bounded byte budget, evicting oldest events FIFO when the budget would be exceeded.

```go
type RingRecorder struct {
    mu        sync.Mutex
    trace     *Trace
    startTime time.Time
    maxBytes  int
    curBytes  int
}

func NewRingRecorder(runID string, command []string, env map[string]string, initialSize Size, maxBytes int) *RingRecorder
func (r *RingRecorder) AddEvent(eventType EventType, data []byte)
func (r *RingRecorder) AddResize(width, height int)
func (r *RingRecorder) AddSignal(sig string)
func (r *RingRecorder) Dump(path string) error
```

`maxBytes` defaults to 8 MB, overridable via `MOAT_TTY_RING_BYTES`. At typical terminal write volume this covers ~5–15 minutes of backlog.

`Dump` snapshots the current event list and writes a standard `Trace` JSON file via the existing `Trace.Save` path. Timestamps are monotonic from session start regardless of eviction (the analyzer treats them as relative).

To let `RecordingWriter`/`RecordingReader` write to either recorder type, extract an interface in `recorder.go`:

```go
type EventRecorder interface {
    AddEvent(eventType EventType, data []byte)
    AddResize(width, height int)
    AddSignal(sig string)
}
```

`*Recorder` and `*RingRecorder` both satisfy it. `RecordingWriter`/`RecordingReader` take `EventRecorder` instead of `*Recorder`. Callers pass either.

### 2. `internal/tui` — `Writer.Reset`

New method on `*Writer`:

```go
func (w *Writer) Reset() error
```

Behavior, under `w.mu`:

1. Stop `footerTimer` if running.
2. If `altScreen`: stop the render loop, exit alt screen (`ESC[?1049l`), drop the emulator, set `altScreen = false`.
3. Emit a soft reset sequence (`ESC[!p` — DECSTR), clear screen (`ESC[2J`), home cursor (`ESC[H`).
4. Reset the `escBuf` partial-escape buffer to empty.
5. Re-run `setupScrollRegionLocked()`, which re-establishes DECSTBM and redraws the footer.

We use soft reset (`ESC[!p`) rather than full RIS (`ESC c`) to avoid clearing the user's scrollback. Soft reset clears the scroll region, character attributes, cursor mode, and saved cursor — enough to recover the cases we expect, without nuking the terminal session itself.

The caller is responsible for nudging the child to redraw — see exec.go wiring.

### 3. `internal/term` — escape proxy keys

Add to `escape.go`:

```go
const (
    EscapeNone EscapeAction = iota
    EscapeStop
    EscapeSnapshot
    EscapeDumpTUI    // new
    EscapeResetTUI   // new
)

const (
    escapeKeyStop     byte = 'k'
    escapeKeySnapshot byte = 's'
    escapeKeyDumpTUI  byte = 'd' // new
    escapeKeyResetTUI byte = 'r' // new
)
```

Both new actions are routed through the existing `onAction` callback path (non-disruptive — they don't unwind `Read()`).

Update `EscapeHelpText()` and the in-session footer hint:

```
ctrl+/ s (snapshot) · k (stop) · d (dump tui) · r (reset tui)
```

## Wire-up in `cmd/moat/cli/exec.go`

`RunInteractiveAttached` changes:

- Always construct a `RingRecorder` for interactive sessions (independent of `--tty-trace`).
- If `--tty-trace` is also set, chain both recorders. `RecordingWriter` and `RecordingReader` are wrappable, so we layer them — each recorder gets its own copy of events.
- The explicit trace recorder remains unbounded (existing behavior). The ring recorder is always bounded.

Add two `OnAction` branches alongside the existing snapshot handler:

```go
case term.EscapeDumpTUI:
    go dumpTUI(r, ringRecorder, statusWriter, &flashMu, &flashTimer)
case term.EscapeResetTUI:
    go resetTUI(ctx, manager, r, statusWriter, &flashMu, &flashTimer)
```

`dumpTUI`:
- Writes to `<run-dir>/tui-debug-<unix-ts>.json` where `<run-dir>` is `r.Dir()` (or equivalent — match the path used by `takeSnapshot`).
- On success, flash the path in the footer using the existing flash mechanism.
- On error, log via `log.Error` and flash `tui dump failed: <err>`.

`resetTUI`:
- Calls `statusWriter.Reset()`.
- Then calls `manager.ResizeTTY(ctx, r.ID, height, width)` with current terminal dimensions to provoke the child to redraw.
- On success, flash `tui reset`.
- On error, log + flash `tui reset failed: <err>`. The session was already broken; don't make it worse.

## Data flow

Reading direction (terminal → container):

```
os.Stdin → escapeProxy → [clipboardProxy?] → ringRecorderReader → [traceRecorderReader?] → container
```

Writing direction (container → terminal):

```
container → ringRecorderWriter → [traceRecorderWriter?] → tuiWriter → os.Stdout
```

The recorders sit between the escape/clipboard layers and the I/O endpoint, so they observe exactly what the child sees on stdin and exactly what bytes the child emits — equivalent to `--tty-trace` semantics today.

## Error handling

- `Dump` failure: log + flash. Session continues.
- `Reset` write failure to terminal: log + flash. Session continues; user can retry or kill.
- `RingRecorder` is best-effort: if eviction has a bug, recordings are wrong but the session is unaffected. No `RingRecorder` error returns from `AddEvent`.
- `MOAT_TTY_RING_BYTES` parse failure: log warning, fall back to default. Don't fail session startup over a bad env var.

## Testing

Unit tests:

- `RingRecorder`:
  - Eviction at byte budget — adding events past `maxBytes` evicts oldest.
  - `Dump` produces a valid `Trace` JSON readable by `trace.Load`.
  - Timestamps monotonic across eviction (the analyzer's resize-issue detector relies on this).
  - Concurrent `AddEvent` from multiple goroutines (race detector).
- `EscapeProxy`:
  - `Ctrl-/ d` fires `OnAction(EscapeDumpTUI)`, no bytes pass through.
  - `Ctrl-/ r` fires `OnAction(EscapeResetTUI)`, no bytes pass through.
  - Match the existing `EscapeSnapshot` test pattern in `escape_test.go`.
- `Writer.Reset`:
  - From scroll mode — emitted bytes contain soft reset, DECSTBM, footer redraw.
  - From alt screen mode — emitted bytes include alt-screen exit before reset, render loop is stopped.
  - Footer timer stopped after `Reset` returns.

Manual verification (no E2E):

- `d` produces a file at the expected path with non-zero events.
- `moat tty-trace analyze <dump>` runs successfully on a dump.
- `r` redraws the footer and triggers Claude Code to redraw.

## Backwards compatibility

- `Trace` JSON format unchanged. Existing `moat tty-trace analyze` consumes dumps without modification.
- `--tty-trace` flag behavior unchanged. The ring recorder runs alongside it.
- `EscapeAction` enum values are append-only (`EscapeDumpTUI`, `EscapeResetTUI` added at the end). No existing key bindings change.
- Footer hint text changes to include `d` and `r`. User-visible string only.

## Out of scope (future work)

- Pulling the dump trigger from a slash command inside Claude Code rather than the Moat menu.
- Including a screenshot of the rendered emulator state alongside the raw event stream in dumps.
- Auto-dump on detected corruption (would need a corruption signal we don't currently have).
