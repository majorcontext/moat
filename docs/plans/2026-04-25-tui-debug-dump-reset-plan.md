# TUI Debug — Dump and Reset Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Ctrl+/ shortcuts `d` (dump TUI history) and `r` (reset terminal) to interactive Moat sessions, backed by an always-on bounded ring buffer of TTY events.

**Architecture:** A new `RingRecorder` in `internal/trace` mirrors the existing `Recorder` API but enforces a byte budget with FIFO eviction. `RecordingWriter` and `RecordingReader` are generalized via a new `EventRecorder` interface so either recorder type works. `internal/tui.Writer` gains a `Reset()` method that emits a soft terminal reset and re-establishes the scroll region/footer. Two new escape actions in `internal/term` route through the existing `OnAction` callback pattern. `cmd/moat/cli/exec.go` wires the ring recorder into every interactive session and handles the two new escape callbacks.

**Tech Stack:** Go, existing `internal/trace`/`internal/tui`/`internal/term` packages. No new dependencies.

**Spec:** `docs/plans/2026-04-25-tui-debug-dump-reset-design.md`

---

## File Map

**Create:**
- `internal/trace/ring.go` — `RingRecorder` type with byte-bounded FIFO eviction.
- `internal/trace/ring_test.go` — unit tests for ring recorder.

**Modify:**
- `internal/trace/recorder.go` — extract `EventRecorder` interface; change `RecordingWriter`/`RecordingReader` to take it.
- `internal/term/escape.go` — add `EscapeDumpTUI`/`EscapeResetTUI` actions and `d`/`r` keys; update `EscapeHelpText`.
- `internal/term/escape_test.go` — adjust the test case using `d` as an unrecognized key; add tests for new actions.
- `internal/tui/writer.go` — add `Reset()` method; update `SetupEscapeHints` menu text.
- `internal/tui/writer_test.go` — add `Reset()` tests.
- `cmd/moat/cli/exec.go` — always-on ring recorder; `dumpTUI`/`resetTUI` handlers; wire new `OnAction` cases.

---

## Task 1: Extract `EventRecorder` interface

**Why:** `RecordingWriter` and `RecordingReader` currently embed `*Recorder`. We need them to also accept the upcoming `*RingRecorder`.

**Files:**
- Modify: `internal/trace/recorder.go`

- [ ] **Step 1: Add the interface and switch the wrappers to use it**

Edit `internal/trace/recorder.go`. Add the interface near the top (right after the imports / `Recorder` declarations) and update the wrapper structs:

```go
// EventRecorder is the minimal surface used by RecordingWriter/RecordingReader.
// Both *Recorder (unbounded) and *RingRecorder (bounded) satisfy it.
type EventRecorder interface {
    AddEvent(eventType EventType, data []byte)
    AddResize(width, height int)
    AddSignal(sig string)
}
```

Change the `RecordingWriter` and `RecordingReader` structs from `recorder *Recorder` to `recorder EventRecorder`, and update the constructors:

```go
type RecordingWriter struct {
    w         io.Writer
    recorder  EventRecorder
    eventType EventType
}

func NewRecordingWriter(w io.Writer, recorder EventRecorder, eventType EventType) io.Writer {
    return &RecordingWriter{w: w, recorder: recorder, eventType: eventType}
}

type RecordingReader struct {
    r         io.Reader
    recorder  EventRecorder
    eventType EventType
}

func NewRecordingReader(r io.Reader, recorder EventRecorder, eventType EventType) io.Reader {
    return &RecordingReader{r: r, recorder: recorder, eventType: eventType}
}
```

The `Write`/`Read` methods don't change — they call `rw.recorder.AddEvent(...)` which the interface satisfies.

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/trace/...`
Expected: success.

- [ ] **Step 3: Run trace tests**

Run: `go test ./internal/trace/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/trace/recorder.go
git commit -m "refactor(trace): extract EventRecorder interface"
```

---

## Task 2: `RingRecorder` — failing test

**Files:**
- Create: `internal/trace/ring_test.go`

- [ ] **Step 1: Write the failing test file**

Create `internal/trace/ring_test.go` with the full content below. It covers eviction, dump format, monotonic timestamps across eviction, and concurrent writes.

```go
package trace

import (
    "bytes"
    "path/filepath"
    "sync"
    "testing"
)

func TestRingRecorder_AddAndDump(t *testing.T) {
    r := NewRingRecorder("run_test", []string{"echo", "hi"}, map[string]string{"TERM": "xterm"}, Size{Width: 80, Height: 24}, 1024)
    r.AddEvent(EventStdout, []byte("hello"))
    r.AddEvent(EventStdin, []byte("a"))
    r.AddResize(120, 40)
    r.AddSignal("SIGWINCH")

    path := filepath.Join(t.TempDir(), "dump.json")
    if err := r.Dump(path); err != nil {
        t.Fatalf("Dump: %v", err)
    }

    loaded, err := Load(path)
    if err != nil {
        t.Fatalf("Load: %v", err)
    }
    if loaded.Metadata.RunID != "run_test" {
        t.Errorf("RunID = %q, want %q", loaded.Metadata.RunID, "run_test")
    }
    if len(loaded.Events) != 4 {
        t.Fatalf("got %d events, want 4", len(loaded.Events))
    }
    if loaded.Events[0].Type != EventStdout || !bytes.Equal(loaded.Events[0].Data, []byte("hello")) {
        t.Errorf("event 0 mismatch: %+v", loaded.Events[0])
    }
    if loaded.Events[2].Type != EventResize || loaded.Events[2].Size == nil || loaded.Events[2].Size.Width != 120 {
        t.Errorf("resize event mismatch: %+v", loaded.Events[2])
    }
    if loaded.Events[3].Signal != "SIGWINCH" {
        t.Errorf("signal event mismatch: %+v", loaded.Events[3])
    }
}

func TestRingRecorder_Eviction(t *testing.T) {
    // Budget 100 bytes — write 10 events of ~30 bytes each => most must be evicted.
    r := NewRingRecorder("run_test", nil, nil, Size{}, 100)
    payload := bytes.Repeat([]byte("x"), 30)
    for i := 0; i < 10; i++ {
        r.AddEvent(EventStdout, payload)
    }

    path := filepath.Join(t.TempDir(), "dump.json")
    if err := r.Dump(path); err != nil {
        t.Fatalf("Dump: %v", err)
    }
    loaded, err := Load(path)
    if err != nil {
        t.Fatalf("Load: %v", err)
    }

    // Total bytes retained must not exceed budget.
    var total int
    for _, e := range loaded.Events {
        total += len(e.Data)
    }
    if total > 100 {
        t.Errorf("retained %d bytes, want <= 100", total)
    }
    if len(loaded.Events) == 10 {
        t.Errorf("no eviction occurred: still have all 10 events")
    }
    if len(loaded.Events) == 0 {
        t.Errorf("evicted everything: ring is empty")
    }
}

func TestRingRecorder_MonotonicTimestamps(t *testing.T) {
    r := NewRingRecorder("run_test", nil, nil, Size{}, 50)
    for i := 0; i < 5; i++ {
        r.AddEvent(EventStdout, bytes.Repeat([]byte("x"), 20))
    }

    path := filepath.Join(t.TempDir(), "dump.json")
    if err := r.Dump(path); err != nil {
        t.Fatalf("Dump: %v", err)
    }
    loaded, err := Load(path)
    if err != nil {
        t.Fatalf("Load: %v", err)
    }

    // After eviction, remaining events must still have non-decreasing timestamps.
    var prev int64 = -1
    for i, e := range loaded.Events {
        if e.TimestampNano < prev {
            t.Errorf("event %d timestamp %d < previous %d (not monotonic)", i, e.TimestampNano, prev)
        }
        prev = e.TimestampNano
    }
}

func TestRingRecorder_ConcurrentAdd(t *testing.T) {
    r := NewRingRecorder("run_test", nil, nil, Size{}, 1<<20)
    var wg sync.WaitGroup
    for i := 0; i < 8; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for j := 0; j < 100; j++ {
                r.AddEvent(EventStdout, []byte("xx"))
            }
        }()
    }
    wg.Wait()

    path := filepath.Join(t.TempDir(), "dump.json")
    if err := r.Dump(path); err != nil {
        t.Fatalf("Dump: %v", err)
    }
    loaded, err := Load(path)
    if err != nil {
        t.Fatalf("Load: %v", err)
    }
    if len(loaded.Events) != 800 {
        t.Errorf("got %d events, want 800", len(loaded.Events))
    }
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internal/trace/ -run RingRecorder`
Expected: build/compile error (NewRingRecorder undefined). This is the failing-test step.

---

## Task 3: `RingRecorder` — implementation

**Files:**
- Create: `internal/trace/ring.go`

- [ ] **Step 1: Implement RingRecorder**

Create `internal/trace/ring.go`:

```go
package trace

import (
    "sync"
    "time"
)

// RingRecorder records events into a bounded byte budget, evicting oldest
// events FIFO when the budget would be exceeded. Used for always-on capture
// of recent TTY activity in interactive sessions, so users can dump on demand
// after a rendering bug manifests.
//
// Only Event.Data bytes count against the budget. Per-event overhead (struct
// size, JSON encoding) is small relative to data and ignored.
type RingRecorder struct {
    mu        sync.Mutex
    trace     *Trace
    startTime time.Time
    maxBytes  int
    curBytes  int
}

// NewRingRecorder creates a ring recorder with the given byte budget.
// maxBytes <= 0 disables eviction (unbounded).
func NewRingRecorder(runID string, command []string, env map[string]string, initialSize Size, maxBytes int) *RingRecorder {
    return &RingRecorder{
        trace: &Trace{
            Metadata: Metadata{
                Timestamp:   time.Now(),
                RunID:       runID,
                Command:     command,
                Environment: env,
                InitialSize: initialSize,
            },
            Events: make([]Event, 0),
        },
        startTime: time.Now(),
        maxBytes:  maxBytes,
    }
}

// AddEvent records an I/O event, evicting oldest events if the byte budget is exceeded.
func (r *RingRecorder) AddEvent(eventType EventType, data []byte) {
    if len(data) == 0 {
        return
    }
    r.mu.Lock()
    defer r.mu.Unlock()

    dataCopy := make([]byte, len(data))
    copy(dataCopy, data)

    r.trace.Events = append(r.trace.Events, Event{
        TimestampNano: time.Since(r.startTime).Nanoseconds(),
        Type:          eventType,
        Data:          dataCopy,
    })
    r.curBytes += len(dataCopy)
    r.evictLocked()
}

// AddResize records a terminal resize event.
func (r *RingRecorder) AddResize(width, height int) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.trace.Events = append(r.trace.Events, Event{
        TimestampNano: time.Since(r.startTime).Nanoseconds(),
        Type:          EventResize,
        Size:          &Size{Width: width, Height: height},
    })
}

// AddSignal records a signal event.
func (r *RingRecorder) AddSignal(sig string) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.trace.Events = append(r.trace.Events, Event{
        TimestampNano: time.Since(r.startTime).Nanoseconds(),
        Type:          EventSignal,
        Signal:        sig,
    })
}

// Dump writes the current ring contents to a file as a Trace JSON.
func (r *RingRecorder) Dump(path string) error {
    r.mu.Lock()
    snapshot := Trace{
        Metadata: r.trace.Metadata,
        Events:   make([]Event, len(r.trace.Events)),
    }
    copy(snapshot.Events, r.trace.Events)
    r.mu.Unlock()
    return snapshot.Save(path)
}

// evictLocked drops oldest events until curBytes <= maxBytes. Caller holds the mutex.
func (r *RingRecorder) evictLocked() {
    if r.maxBytes <= 0 || r.curBytes <= r.maxBytes {
        return
    }
    drop := 0
    for drop < len(r.trace.Events) && r.curBytes > r.maxBytes {
        r.curBytes -= len(r.trace.Events[drop].Data)
        drop++
    }
    if drop > 0 {
        r.trace.Events = r.trace.Events[drop:]
    }
}
```

- [ ] **Step 2: Run tests and verify they pass**

Run: `go test -race ./internal/trace/ -run RingRecorder`
Expected: PASS, no data races.

- [ ] **Step 3: Run all trace tests**

Run: `go test ./internal/trace/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/trace/ring.go internal/trace/ring_test.go
git commit -m "feat(trace): add bounded RingRecorder for on-demand dumps"
```

---

## Task 4: Escape proxy — new actions and keys

**Files:**
- Modify: `internal/term/escape.go`
- Modify: `internal/term/escape_test.go`

- [ ] **Step 1: Update existing test that uses `d` as unrecognized**

In `internal/term/escape_test.go` around line 258, the test case `"prefix detected then canceled with unrecognized d"` uses `d` as an unrecognized key. Since `d` is now a recognized key, replace `'d'` with `'q'` and update the test name:

```go
{
    name:           "prefix detected then canceled with unrecognized q",
    input:          []byte{EscapePrefix, 'q'},
    wantCallbacks:  []bool{true, false},
    wantFinalState: false,
},
```

- [ ] **Step 2: Add failing tests for the two new actions**

Append to `internal/term/escape_test.go`:

```go
func TestEscapeProxy_DumpTUI(t *testing.T) {
    input := []byte{EscapePrefix, 'd'}
    r := NewEscapeProxy(bytes.NewReader(input))

    var gotAction EscapeAction
    r.OnAction(func(action EscapeAction) {
        gotAction = action
    })

    buf := make([]byte, 10)
    _, err := r.Read(buf)
    if IsEscapeError(err) {
        t.Fatalf("dump should not return EscapeError, got: %v", err)
    }
    if gotAction != EscapeDumpTUI {
        t.Errorf("expected EscapeDumpTUI callback, got: %v", gotAction)
    }
}

func TestEscapeProxy_ResetTUI(t *testing.T) {
    input := []byte{EscapePrefix, 'r'}
    r := NewEscapeProxy(bytes.NewReader(input))

    var gotAction EscapeAction
    r.OnAction(func(action EscapeAction) {
        gotAction = action
    })

    buf := make([]byte, 10)
    _, err := r.Read(buf)
    if IsEscapeError(err) {
        t.Fatalf("reset should not return EscapeError, got: %v", err)
    }
    if gotAction != EscapeResetTUI {
        t.Errorf("expected EscapeResetTUI callback, got: %v", gotAction)
    }
}

func TestEscapeProxy_DumpAndResetContinueReading(t *testing.T) {
    // After Ctrl-/ d and Ctrl-/ r, surrounding data flows through.
    input := []byte{'a', EscapePrefix, 'd', 'b', EscapePrefix, 'r', 'c'}
    r := NewEscapeProxy(bytes.NewReader(input))

    var actions []EscapeAction
    r.OnAction(func(action EscapeAction) {
        actions = append(actions, action)
    })

    out, err := io.ReadAll(r)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    expected := []byte{'a', 'b', 'c'}
    if !bytes.Equal(out, expected) {
        t.Errorf("got %q, want %q", out, expected)
    }
    if len(actions) != 2 || actions[0] != EscapeDumpTUI || actions[1] != EscapeResetTUI {
        t.Errorf("expected [EscapeDumpTUI, EscapeResetTUI], got %v", actions)
    }
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/term/ -run "EscapeProxy_DumpTUI|EscapeProxy_ResetTUI|EscapeProxy_DumpAndResetContinueReading"`
Expected: build error (`EscapeDumpTUI`/`EscapeResetTUI` undefined).

- [ ] **Step 4: Add the new actions and key bindings**

Edit `internal/term/escape.go`:

```go
const (
    EscapeNone EscapeAction = iota
    EscapeStop
    EscapeSnapshot
    EscapeDumpTUI
    EscapeResetTUI
)
```

Update `(e EscapeError) Error()`:

```go
func (e EscapeError) Error() string {
    switch e.Action {
    case EscapeStop:
        return "escape: stop"
    case EscapeSnapshot:
        return "escape: snapshot"
    case EscapeDumpTUI:
        return "escape: dump-tui"
    case EscapeResetTUI:
        return "escape: reset-tui"
    default:
        return "escape: unknown"
    }
}
```

Add the key constants near `escapeKeyStop`/`escapeKeySnapshot`:

```go
const (
    EscapePrefix byte = 0x1f

    escapeKeyStop     byte = 'k'
    escapeKeySnapshot byte = 's'
    escapeKeyDumpTUI  byte = 'd'
    escapeKeyResetTUI byte = 'r'
)
```

In the two `Read()` switch statements (the one inside the for loop and the one in the "consumed all input ended on prefix" branch), add cases alongside `escapeKeySnapshot`. Both new actions are non-disruptive (call `onAction`, continue reading) — same shape as snapshot:

In the in-loop switch (around line 168):

```go
case escapeKeySnapshot:
    if e.onAction != nil {
        e.onAction(EscapeSnapshot)
    }

case escapeKeyDumpTUI:
    if e.onAction != nil {
        e.onAction(EscapeDumpTUI)
    }

case escapeKeyResetTUI:
    if e.onAction != nil {
        e.onAction(EscapeResetTUI)
    }
```

In the "one more byte" branch (around line 247):

```go
case escapeKeySnapshot:
    if e.onAction != nil {
        e.onAction(EscapeSnapshot)
    }
    return 0, nil
case escapeKeyDumpTUI:
    if e.onAction != nil {
        e.onAction(EscapeDumpTUI)
    }
    return 0, nil
case escapeKeyResetTUI:
    if e.onAction != nil {
        e.onAction(EscapeResetTUI)
    }
    return 0, nil
```

Update `EscapeHelpText()`:

```go
func EscapeHelpText() string {
    return "ctrl+/ s (snapshot) · k (stop) · d (dump tui) · r (reset tui)"
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/term/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/term/escape.go internal/term/escape_test.go
git commit -m "feat(term): add ctrl+/ d and r escape actions for TUI debug"
```

---

## Task 5: `Writer.Reset` — failing test

**Files:**
- Modify: `internal/tui/writer_test.go`

- [ ] **Step 1: Read the existing test file to match style**

Run: `head -60 internal/tui/writer_test.go`
Note the helpers (e.g., how a `Writer` is constructed, how output is captured via a `bytes.Buffer`, and the existing `StatusBar` constructor).

- [ ] **Step 2: Add failing tests for Reset**

Append to `internal/tui/writer_test.go`. Use the same construction pattern as the surrounding tests. The exact `NewStatusBar`/`NewWriter` signatures are visible at the top of the file — match them.

```go
func TestWriter_Reset_ScrollMode(t *testing.T) {
    var out bytes.Buffer
    bar := NewStatusBar(80, 24)
    w := NewWriter(&out, bar, "docker")

    if err := w.Setup(); err != nil {
        t.Fatalf("Setup: %v", err)
    }
    out.Reset()

    if err := w.Reset(); err != nil {
        t.Fatalf("Reset: %v", err)
    }

    written := out.String()
    // Soft reset
    if !strings.Contains(written, "\x1b[!p") {
        t.Errorf("expected soft reset (ESC[!p), got %q", written)
    }
    // DECSTBM scroll region (re-established by setupScrollRegionLocked)
    if !strings.Contains(written, "\x1b[1;23r") {
        t.Errorf("expected DECSTBM 1;23r, got %q", written)
    }
}

func TestWriter_Reset_StopsFooterTimer(t *testing.T) {
    var out bytes.Buffer
    bar := NewStatusBar(80, 24)
    w := NewWriter(&out, bar, "docker")
    if err := w.Setup(); err != nil {
        t.Fatalf("Setup: %v", err)
    }

    // Trigger a footer redraw schedule by writing some output.
    _, _ = w.Write([]byte("hello"))

    if err := w.Reset(); err != nil {
        t.Fatalf("Reset: %v", err)
    }

    w.mu.Lock()
    timerActive := w.footerTimer != nil
    w.mu.Unlock()
    if timerActive {
        t.Errorf("footerTimer should be cleared after Reset")
    }
}

func TestWriter_Reset_ExitsAltScreen(t *testing.T) {
    var out bytes.Buffer
    bar := NewStatusBar(80, 24)
    w := NewWriter(&out, bar, "docker")
    if err := w.Setup(); err != nil {
        t.Fatalf("Setup: %v", err)
    }
    // Enter alt screen by writing the enter sequence.
    if _, err := w.Write([]byte("\x1b[?1049h")); err != nil {
        t.Fatalf("Write alt-screen enter: %v", err)
    }
    out.Reset()

    if err := w.Reset(); err != nil {
        t.Fatalf("Reset: %v", err)
    }

    written := out.String()
    if !strings.Contains(written, "\x1b[?1049l") {
        t.Errorf("expected alt-screen exit (ESC[?1049l), got %q", written)
    }

    w.mu.Lock()
    inAlt := w.altScreen
    hasEmu := w.emulator != nil
    w.mu.Unlock()
    if inAlt {
        t.Errorf("altScreen should be false after Reset")
    }
    if hasEmu {
        t.Errorf("emulator should be nil after Reset")
    }
}
```

If `strings` isn't imported in this file, add it.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run "Writer_Reset"`
Expected: build error (`w.Reset undefined`).

---

## Task 6: `Writer.Reset` — implementation

**Files:**
- Modify: `internal/tui/writer.go`

- [ ] **Step 1: Add the Reset method**

Insert this method in `internal/tui/writer.go` near `Cleanup`:

```go
// Reset attempts to recover the terminal from a corrupted state. It exits
// alternate screen mode if active, drops the VT emulator, emits a soft
// terminal reset (DECSTR), clears the screen, and re-establishes the scroll
// region and footer.
//
// Soft reset (ESC[!p) is used rather than full RIS (ESC c) so the user's
// scrollback is preserved. The caller is responsible for nudging the child
// process to redraw (typically via a no-op TTY resize).
func (w *Writer) Reset() error {
    w.mu.Lock()
    defer w.mu.Unlock()

    // Stop footer timer
    if w.footerTimer != nil {
        w.footerTimer.Stop()
        w.footerTimer = nil
    }

    var buf bytes.Buffer

    // If in compositor mode, exit it cleanly.
    if w.altScreen {
        w.stopRenderLoop()
        w.emulator = nil
        w.altScreen = false
        buf.WriteString("\x1b[?1049l") // exit alt screen
    }

    // Clear partial-escape buffer; any in-flight sequence is invalid after reset.
    w.escBuf = nil

    // Soft terminal reset: clears scroll region, attributes, modes, saved cursor.
    buf.WriteString("\x1b[!p")
    // Clear screen and home cursor.
    buf.WriteString("\x1b[2J\x1b[H")
    // Show cursor (in case the child had hidden it).
    buf.WriteString("\x1b[?25h")

    if _, err := w.out.Write(buf.Bytes()); err != nil {
        return err
    }

    // Re-establish scroll region and redraw footer.
    return w.setupScrollRegionLocked()
}
```

- [ ] **Step 2: Run Reset tests**

Run: `go test ./internal/tui/ -run "Writer_Reset"`
Expected: PASS.

- [ ] **Step 3: Run all TUI tests**

Run: `go test -race ./internal/tui/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/tui/writer.go internal/tui/writer_test.go
git commit -m "feat(tui): add Writer.Reset for terminal recovery"
```

---

## Task 7: Wire ring recorder + dump/reset handlers in exec.go

**Files:**
- Modify: `cmd/moat/cli/exec.go`

- [ ] **Step 1: Add the always-on ring recorder construction**

In `RunInteractiveAttached` (currently around line 354), after `setupTTYTracer` returns and before the status-bar setup, build a `RingRecorder` keyed off the run:

```go
// Always-on bounded ring buffer for on-demand TUI debug dumps.
ringBytes := defaultRingBytes
if env := os.Getenv("MOAT_TTY_RING_BYTES"); env != "" {
    if n, err := strconv.Atoi(env); err == nil && n > 0 {
        ringBytes = n
    } else {
        log.Warn("invalid MOAT_TTY_RING_BYTES, using default", "value", env, "default", defaultRingBytes)
    }
}
width, height := 80, 24
if term.IsTerminal(os.Stdout) {
    if w, h := term.GetSize(os.Stdout); w > 0 && h > 0 {
        width, height = w, h
    }
}
ringRecorder := trace.NewRingRecorder(r.ID, command, trace.GetTraceEnv(), trace.Size{Width: width, Height: height}, ringBytes)
```

Add `defaultRingBytes` as a package-level const near the top of the file:

```go
// defaultRingBytes is the default byte budget for the always-on TUI debug ring
// buffer. ~5–15 minutes of typical terminal output. Override with
// MOAT_TTY_RING_BYTES.
const defaultRingBytes = 8 * 1024 * 1024
```

Add `"strconv"` to the imports if not already present.

- [ ] **Step 2: Wrap stdout/stdin with the ring recorder**

After the existing `if tracer != nil { stdout = trace.NewRecordingWriter(...) }`, also wrap with the ring (always):

```go
stdout = trace.NewRecordingWriter(stdout, ringRecorder, trace.EventStdout)
```

After the existing `if tracer != nil { stdin = trace.NewRecordingReader(...) }`, also wrap with the ring (always):

```go
stdin = trace.NewRecordingReader(stdin, ringRecorder, trace.EventStdin)
```

Order: recorders should sit *after* the escape/clipboard wrappers (so they see the bytes the child actually receives), matching the existing `tracer` placement. Make sure `ringRecorder` wraps occur in the same position the `tracer` wraps already do.

- [ ] **Step 3: Add the SIGWINCH path's ring resize event**

In the existing `case sig := <-sigCh:` block, where `tracer.recorder.AddResize(...)` is called on resize, add the ring as well:

```go
ringRecorder.AddResize(width, height)
```

(Place this immediately above or below the existing `tracer != nil` resize call.)

- [ ] **Step 4: Add the dump and reset handlers**

Append to `cmd/moat/cli/exec.go`:

```go
// dumpTUI saves the in-memory TTY ring buffer to disk and flashes the path.
func dumpTUI(r *run.Run, ringRecorder *trace.RingRecorder, statusWriter *tui.Writer, flashMu *sync.Mutex, flashTimer **time.Timer) {
    flash := func(msg string) {
        if statusWriter == nil {
            return
        }
        flashMu.Lock()
        defer flashMu.Unlock()
        if *flashTimer != nil {
            (*flashTimer).Stop()
        }
        statusWriter.SetMessage(msg)
        _ = statusWriter.UpdateStatus()
        *flashTimer = time.AfterFunc(2*time.Second, func() {
            statusWriter.ClearMessage()
            _ = statusWriter.UpdateStatus()
        })
    }

    runDir := filepath.Join(storage.DefaultBaseDir(), r.ID)
    path := filepath.Join(runDir, fmt.Sprintf("tui-debug-%d.json", time.Now().Unix()))
    if err := ringRecorder.Dump(path); err != nil {
        log.Error("tui dump failed", "path", path, "error", err)
        flash("tui dump failed: " + err.Error())
        return
    }
    log.Info("tui dump saved", "path", path)
    flash("tui dump saved: " + path)
}

// resetTUI emits a soft terminal reset and nudges the container to redraw.
func resetTUI(ctx context.Context, manager *run.Manager, r *run.Run, statusWriter *tui.Writer, flashMu *sync.Mutex, flashTimer **time.Timer) {
    flash := func(msg string) {
        if statusWriter == nil {
            return
        }
        flashMu.Lock()
        defer flashMu.Unlock()
        if *flashTimer != nil {
            (*flashTimer).Stop()
        }
        statusWriter.SetMessage(msg)
        _ = statusWriter.UpdateStatus()
        *flashTimer = time.AfterFunc(2*time.Second, func() {
            statusWriter.ClearMessage()
            _ = statusWriter.UpdateStatus()
        })
    }

    if statusWriter == nil {
        flash("tui reset: no status writer")
        return
    }
    if err := statusWriter.Reset(); err != nil {
        log.Error("tui reset failed", "error", err)
        flash("tui reset failed: " + err.Error())
        return
    }

    // Nudge the container's TUI to redraw via a no-op resize.
    if term.IsTerminal(os.Stdout) {
        if width, height := term.GetSize(os.Stdout); width > 0 && height > 0 {
            // #nosec G115 -- width/height validated positive
            if err := manager.ResizeTTY(ctx, r.ID, uint(height), uint(width)); err != nil {
                log.Debug("post-reset resize nudge failed", "error", err)
            }
        }
    }

    flash("tui reset")
}
```

Imports to verify present at the top of the file: `"path/filepath"`, `"github.com/majorcontext/moat/internal/storage"`, `"github.com/majorcontext/moat/internal/trace"`, `"github.com/majorcontext/moat/internal/tui"`.

- [ ] **Step 5: Wire the new actions into OnAction**

Replace the existing `escapeProxy.OnAction(...)` block:

```go
escapeProxy.OnAction(func(action term.EscapeAction) {
    switch action {
    case term.EscapeSnapshot:
        go takeSnapshot(r, statusWriter, &flashMu, &flashTimer)
    case term.EscapeDumpTUI:
        go dumpTUI(r, ringRecorder, statusWriter, &flashMu, &flashTimer)
    case term.EscapeResetTUI:
        go resetTUI(ctx, manager, r, statusWriter, &flashMu, &flashTimer)
    }
})
```

- [ ] **Step 6: Update the in-session menu hint**

Find the `SetupEscapeHints` callsite and the `SetMessage` in `internal/tui/writer.go`'s `SetupEscapeHints`. Update the message to reflect the new keys:

```go
w.SetMessage("s (snapshot) · k (stop) · d (dump tui) · r (reset tui) · ctrl+/ (cancel)")
```

- [ ] **Step 7: Build the project**

Run: `go build ./...`
Expected: success.

- [ ] **Step 8: Run unit tests with race detector**

Run: `make test-unit`
Expected: PASS. (If `make test-unit` is unavailable in this env, run `go test -race ./...` instead.)

- [ ] **Step 9: Run the linter**

Run: `make lint`
Expected: PASS. (If unavailable, fall back to `go vet ./...`.)

- [ ] **Step 10: Commit**

```bash
git add cmd/moat/cli/exec.go internal/tui/writer.go
git commit -m "feat(tui): wire ctrl+/ d (dump) and r (reset) handlers"
```

---

## Task 8: Documentation

**Files:**
- Modify: `docs/content/reference/01-cli.md` (if it documents the Ctrl+/ menu)
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Check current docs for the Ctrl+/ menu**

Run: `grep -n "ctrl+/\|snapshot\|escape" docs/content/reference/01-cli.md docs/content/guides/*.md 2>/dev/null | head -20`

If a guide or reference page documents the existing `s` and `k` keys, update it to include `d` (dump tui) and `r` (reset tui), with one sentence each describing what they do and where the dump file is written.

If no doc currently lists the Ctrl+/ menu keys, add a short subsection in the most appropriate guide (likely `docs/content/guides/`) — but do not invent a new top-level page. If unsure, skip and ask reviewer.

- [ ] **Step 2: Add a CHANGELOG entry**

Edit `CHANGELOG.md`. Under the next-release `### Added` section (create one if it doesn't exist):

```markdown
- TUI debug shortcuts — Ctrl+/ d dumps a snapshot of recent terminal I/O to `~/.moat/runs/<id>/tui-debug-<ts>.json` for offline analysis; Ctrl+/ r issues a soft terminal reset and nudges the child to redraw. The dump uses the existing `moat tty-trace analyze` format. Ring buffer size is 8 MB by default, tunable via `MOAT_TTY_RING_BYTES`. ([#NNN](https://github.com/majorcontext/moat/pull/NNN))
```

(Replace `NNN` with the PR number when the PR is opened, or leave the placeholder for the PR-creation step.)

- [ ] **Step 3: Commit**

```bash
git add CHANGELOG.md docs/content/
git commit -m "docs: document ctrl+/ d and r tui debug shortcuts"
```

---

## Task 9: Manual verification

This isn't a unit test step — it's the human-in-the-loop check before merging. The bug we're targeting can't be reproduced on demand, so we verify the *mechanism* works.

- [ ] **Step 1: Build a binary**

Run: `go build -o /tmp/moat ./cmd/moat`
Expected: success.

- [ ] **Step 2: Start an interactive Claude session**

Run: `/tmp/moat claude` in a workspace.
Expected: status bar appears at the bottom; Ctrl+/ shows `s (snapshot) · k (stop) · d (dump tui) · r (reset tui) · ctrl+/ (cancel)`.

- [ ] **Step 3: Verify dump**

Press Ctrl+/ then `d`. Expected: footer flashes a path like `tui dump saved: /Users/.../.moat/runs/run_<id>/tui-debug-<ts>.json`. Open that file and confirm it's valid JSON with `metadata` and `events` fields. Run `/tmp/moat tty-trace analyze <path>` and confirm it produces output.

- [ ] **Step 4: Verify reset**

Send a deliberately corrupting sequence to the terminal (e.g., from inside the running child, `printf '\x1b[10;15r'` to set a bogus scroll region), then press Ctrl+/ then `r`. Expected: terminal redraws, footer reappears at the bottom, child UI re-renders. Footer flashes `tui reset`.

- [ ] **Step 5: Verify ring eviction**

Set `MOAT_TTY_RING_BYTES=65536`, start a session, generate >64 KB of output (e.g., `seq 1 10000`), press Ctrl+/ d. Expected: dumped file's data size sums to ≤ 64 KB, but contains the most recent events.

---

## Self-Review Notes

Spec coverage check:
- RingRecorder type + bounds + dump → Tasks 2–3.
- `EventRecorder` interface for chaining → Task 1.
- `Writer.Reset` with soft reset → Tasks 5–6.
- New escape actions/keys → Task 4.
- Always-on wiring + dump/reset handlers + nudge → Task 7.
- Menu/help text → Task 4 (`EscapeHelpText`) + Task 7 step 6 (`SetupEscapeHints`).
- Env var override + 8 MB default → Task 7 step 1.
- Docs + CHANGELOG → Task 8.
- Manual verification (no E2E for the bug) → Task 9.
- Backwards-compat (Trace JSON, `--tty-trace` flag, append-only enum) → preserved by construction; no separate task needed.

Ambiguity resolved: the snapshot path follows the existing `filepath.Join(storage.DefaultBaseDir(), r.ID)` convention used by `cmd/moat/cli/snapshot.go`.
