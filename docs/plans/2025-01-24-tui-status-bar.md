# TUI Status Bar Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Display a fixed status bar at the bottom of the terminal during interactive moat sessions showing run context.

**Architecture:** New `internal/tui` package with `StatusBar` (renders bar content) and `Writer` (wraps stdout, triggers redraws). Integration at CLI layer in `exec.go` and `attach.go`. Uses ANSI escape sequences for scrolling region and cursor control.

**Tech Stack:** Go stdlib (`io`, `os`, `os/signal`), ANSI escape sequences, existing `internal/term` package for TTY detection.

---

## Task 1: Create StatusBar Core

**Files:**
- Create: `internal/tui/statusbar.go`
- Create: `internal/tui/statusbar_test.go`

**Step 1: Write the failing test**

```go
// internal/tui/statusbar_test.go
package tui

import "testing"

func TestStatusBar_Render(t *testing.T) {
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetWidth(60)

	rendered := bar.Content()

	// Should contain all components
	if !contains(rendered, "moat") {
		t.Errorf("expected 'moat' in bar, got %q", rendered)
	}
	if !contains(rendered, "run_abc123") {
		t.Errorf("expected run ID in bar, got %q", rendered)
	}
	if !contains(rendered, "my-agent") {
		t.Errorf("expected name in bar, got %q", rendered)
	}
	if !contains(rendered, "docker") {
		t.Errorf("expected runtime in bar, got %q", rendered)
	}
}

func TestStatusBar_NarrowWidth(t *testing.T) {
	bar := NewStatusBar("run_abc123", "very-long-agent-name-that-should-be-truncated", "docker")
	bar.SetWidth(40)

	rendered := bar.Content()

	// Run ID should be preserved, name may be truncated
	if !contains(rendered, "run_abc123") {
		t.Errorf("expected run ID preserved in narrow bar, got %q", rendered)
	}
	if len(rendered) > 40 {
		t.Errorf("expected bar width <= 40, got %d", len(rendered))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v ./internal/tui/`
Expected: FAIL - package doesn't exist

**Step 3: Write minimal implementation**

```go
// internal/tui/statusbar.go
package tui

import (
	"fmt"
	"strings"
)

// StatusBar renders run context at the bottom of the terminal.
type StatusBar struct {
	runID   string
	name    string
	runtime string
	width   int
}

// NewStatusBar creates a status bar with the given run metadata.
func NewStatusBar(runID, name, runtime string) *StatusBar {
	return &StatusBar{
		runID:   runID,
		name:    name,
		runtime: runtime,
		width:   80, // default
	}
}

// SetWidth sets the terminal width for rendering.
func (s *StatusBar) SetWidth(width int) {
	s.width = width
}

// Content returns the status bar content string (without ANSI escapes).
func (s *StatusBar) Content() string {
	// Build content: ─── moat │ run_id │ name │ runtime ───...
	content := fmt.Sprintf("─── moat │ %s │ %s │ %s ", s.runID, s.name, s.runtime)

	// Calculate available space for padding
	contentLen := runeLen(content)
	if contentLen >= s.width {
		// Need to truncate - prioritize run ID over name
		return s.truncatedContent()
	}

	// Pad with box-drawing dashes
	padding := s.width - contentLen
	return content + strings.Repeat("─", padding)
}

// truncatedContent returns a shortened version for narrow terminals.
func (s *StatusBar) truncatedContent() string {
	// Minimum: ─── moat │ run_id │ runtime ───
	minimal := fmt.Sprintf("─── moat │ %s │ %s ", s.runID, s.runtime)
	minLen := runeLen(minimal)

	if minLen >= s.width {
		// Even minimal doesn't fit, just show what we can
		if s.width > 4 {
			return minimal[:s.width-3] + "───"
		}
		return strings.Repeat("─", s.width)
	}

	// Add name if space permits, truncating if needed
	nameSpace := s.width - minLen - 4 // " │ " + some padding
	if nameSpace > 3 && len(s.name) > 0 {
		name := s.name
		if len(name) > nameSpace {
			name = name[:nameSpace-1] + "…"
		}
		content := fmt.Sprintf("─── moat │ %s │ %s │ %s ", s.runID, name, s.runtime)
		contentLen := runeLen(content)
		if contentLen < s.width {
			return content + strings.Repeat("─", s.width-contentLen)
		}
		return content
	}

	// Just minimal with padding
	return minimal + strings.Repeat("─", s.width-minLen)
}

// runeLen returns the display width of a string (counting runes).
func runeLen(s string) int {
	return len([]rune(s))
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v ./internal/tui/`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/tui/statusbar.go internal/tui/statusbar_test.go
git commit -m "feat(tui): add StatusBar for run context display"
```

---

## Task 2: Add ANSI Escape Rendering

**Files:**
- Modify: `internal/tui/statusbar.go`
- Modify: `internal/tui/statusbar_test.go`

**Step 1: Write the failing test**

```go
// Add to internal/tui/statusbar_test.go

func TestStatusBar_SetupScrollRegion(t *testing.T) {
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(80, 24) // 80 cols, 24 rows

	setup := bar.SetupScrollRegion()

	// Should set scroll region to rows 1-23 (excluding bottom row)
	if !contains(setup, "\x1b[1;23r") {
		t.Errorf("expected scroll region escape, got %q", setup)
	}
}

func TestStatusBar_RenderEscaped(t *testing.T) {
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(60, 24)

	rendered := bar.Render()

	// Should contain cursor positioning
	if !contains(rendered, "\x1b[24;1H") { // move to row 24, col 1
		t.Errorf("expected cursor move escape, got %q", rendered)
	}
	// Should contain clear line
	if !contains(rendered, "\x1b[2K") {
		t.Errorf("expected clear line escape, got %q", rendered)
	}
}

func TestStatusBar_Cleanup(t *testing.T) {
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(80, 24)

	cleanup := bar.Cleanup()

	// Should reset scroll region to full terminal
	if !contains(cleanup, "\x1b[r") {
		t.Errorf("expected scroll region reset, got %q", cleanup)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v ./internal/tui/`
Expected: FAIL - SetDimensions, Render, Cleanup don't exist

**Step 3: Write minimal implementation**

```go
// Add to internal/tui/statusbar.go

// SetDimensions sets terminal width and height.
func (s *StatusBar) SetDimensions(width, height int) {
	s.width = width
	s.height = height
}

// SetupScrollRegion returns ANSI escapes to set the scrolling region,
// excluding the bottom row for the status bar.
func (s *StatusBar) SetupScrollRegion() string {
	if s.height <= 1 {
		return ""
	}
	// Set scroll region to rows 1 through (height-1)
	return fmt.Sprintf("\x1b[1;%dr", s.height-1)
}

// Render returns the full status bar with ANSI escapes for positioning.
func (s *StatusBar) Render() string {
	if s.height <= 0 {
		return ""
	}
	// Save cursor, move to bottom row, clear line, draw bar, restore cursor
	return fmt.Sprintf("\x1b[s\x1b[%d;1H\x1b[2K%s\x1b[u", s.height, s.Content())
}

// Cleanup returns ANSI escapes to reset terminal state.
func (s *StatusBar) Cleanup() string {
	// Reset scroll region, move to bottom, clear line
	return fmt.Sprintf("\x1b[r\x1b[%d;1H\x1b[2K", s.height)
}

// Add height field to StatusBar struct
```

Update the struct:

```go
type StatusBar struct {
	runID   string
	name    string
	runtime string
	width   int
	height  int
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v ./internal/tui/`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/tui/statusbar.go internal/tui/statusbar_test.go
git commit -m "feat(tui): add ANSI escape rendering to StatusBar"
```

---

## Task 3: Create Wrapped Writer

**Files:**
- Create: `internal/tui/writer.go`
- Create: `internal/tui/writer_test.go`

**Step 1: Write the failing test**

```go
// internal/tui/writer_test.go
package tui

import (
	"bytes"
	"testing"
)

func TestWriter_Write(t *testing.T) {
	var buf bytes.Buffer
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(60, 24)

	w := NewWriter(&buf, bar)

	n, err := w.Write([]byte("hello world\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 12 {
		t.Errorf("expected n=12, got %d", n)
	}

	output := buf.String()

	// Should contain the original content
	if !contains(output, "hello world") {
		t.Errorf("expected original content in output")
	}

	// Should contain status bar render (cursor positioning)
	if !contains(output, "\x1b[s") { // save cursor
		t.Errorf("expected status bar render after write")
	}
}

func TestWriter_Setup(t *testing.T) {
	var buf bytes.Buffer
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(60, 24)

	w := NewWriter(&buf, bar)
	w.Setup()

	output := buf.String()

	// Should set up scroll region
	if !contains(output, "\x1b[1;23r") {
		t.Errorf("expected scroll region setup, got %q", output)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v ./internal/tui/`
Expected: FAIL - NewWriter doesn't exist

**Step 3: Write minimal implementation**

```go
// internal/tui/writer.go
package tui

import "io"

// Writer wraps an io.Writer and redraws the status bar after each write.
type Writer struct {
	w   io.Writer
	bar *StatusBar
}

// NewWriter creates a Writer that redraws the status bar after each write.
func NewWriter(w io.Writer, bar *StatusBar) *Writer {
	return &Writer{w: w, bar: bar}
}

// Setup initializes the terminal for status bar display.
// Call this before any writes to set up the scroll region.
func (w *Writer) Setup() error {
	_, err := w.w.Write([]byte(w.bar.SetupScrollRegion()))
	if err != nil {
		return err
	}
	// Draw initial status bar
	_, err = w.w.Write([]byte(w.bar.Render()))
	return err
}

// Write writes data to the underlying writer and redraws the status bar.
func (w *Writer) Write(p []byte) (n int, err error) {
	// Write the actual content
	n, err = w.w.Write(p)
	if err != nil {
		return n, err
	}

	// Redraw status bar
	_, _ = w.w.Write([]byte(w.bar.Render()))

	return n, nil
}

// Cleanup resets the terminal state.
func (w *Writer) Cleanup() error {
	_, err := w.w.Write([]byte(w.bar.Cleanup()))
	return err
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v ./internal/tui/`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/tui/writer.go internal/tui/writer_test.go
git commit -m "feat(tui): add Writer wrapper for status bar redraws"
```

---

## Task 4: Add SIGWINCH Handling

**Files:**
- Modify: `internal/tui/writer.go`
- Modify: `internal/tui/writer_test.go`

**Step 1: Write the failing test**

```go
// Add to internal/tui/writer_test.go

func TestWriter_Resize(t *testing.T) {
	var buf bytes.Buffer
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(60, 24)

	w := NewWriter(&buf, bar)
	w.Setup()
	buf.Reset() // Clear setup output

	// Simulate resize
	w.Resize(80, 30)

	output := buf.String()

	// Should update scroll region to new height
	if !contains(output, "\x1b[1;29r") { // rows 1-29, excluding row 30
		t.Errorf("expected updated scroll region, got %q", output)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -v ./internal/tui/`
Expected: FAIL - Resize doesn't exist

**Step 3: Write minimal implementation**

```go
// Add to internal/tui/writer.go

// Resize updates the terminal dimensions and redraws.
func (w *Writer) Resize(width, height int) error {
	w.bar.SetDimensions(width, height)

	// Update scroll region
	_, err := w.w.Write([]byte(w.bar.SetupScrollRegion()))
	if err != nil {
		return err
	}

	// Redraw status bar at new position
	_, err = w.w.Write([]byte(w.bar.Render()))
	return err
}
```

**Step 4: Run test to verify it passes**

Run: `go test -v ./internal/tui/`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/tui/writer.go internal/tui/writer_test.go
git commit -m "feat(tui): add Resize support for SIGWINCH handling"
```

---

## Task 5: Integrate with exec.go

**Files:**
- Modify: `cmd/moat/cli/exec.go`

**Step 1: Understand integration points**

Look at `RunInteractiveAttached` function - this is where we'll wrap stdout.

**Step 2: Add integration**

```go
// Modify RunInteractiveAttached in cmd/moat/cli/exec.go

// Add import
import (
	// ... existing imports
	"github.com/andybons/moat/internal/tui"
)

// In RunInteractiveAttached, after raw mode setup and before StartAttached:

// Set up status bar if stdout is a TTY
var statusWriter *tui.Writer
if term.IsTerminal(os.Stdout) {
	width, height := term.GetSize(os.Stdout)
	if width > 0 && height > 0 {
		bar := tui.NewStatusBar(r.ID, r.Name, manager.RuntimeType())
		bar.SetDimensions(width, height)
		statusWriter = tui.NewWriter(os.Stdout, bar)
		if err := statusWriter.Setup(); err != nil {
			log.Debug("failed to setup status bar", "error", err)
			statusWriter = nil
		}
	}
}

// Ensure cleanup
if statusWriter != nil {
	defer statusWriter.Cleanup()
}

// Use statusWriter or os.Stdout for attachment
var stdout io.Writer = os.Stdout
if statusWriter != nil {
	stdout = statusWriter
}

// Pass stdout to StartAttached instead of os.Stdout
```

**Step 3: Add RuntimeType to Manager**

Need to expose runtime type from manager. Check if it already exists, otherwise add to `internal/run/manager.go`:

```go
// RuntimeType returns the container runtime type (docker or apple).
func (m *Manager) RuntimeType() string {
	return string(m.runtime.Type())
}
```

**Step 4: Run the application manually to test**

Run: `go build ./cmd/moat && ./moat run -i -- bash`
Expected: Status bar appears at bottom

**Step 5: Commit**

```bash
git add cmd/moat/cli/exec.go internal/run/manager.go
git commit -m "feat(tui): integrate status bar with interactive run"
```

---

## Task 6: Integrate with attach.go

**Files:**
- Modify: `cmd/moat/cli/attach.go`

**Step 1: Add status bar to attachInteractiveMode**

Similar integration as exec.go:

```go
// In attachInteractiveMode, after raw mode setup:

// Set up status bar if stdout is a TTY
var statusWriter *tui.Writer
if term.IsTerminal(os.Stdout) {
	width, height := term.GetSize(os.Stdout)
	if width > 0 && height > 0 {
		bar := tui.NewStatusBar(r.ID, r.Name, manager.RuntimeType())
		bar.SetDimensions(width, height)
		statusWriter = tui.NewWriter(os.Stdout, bar)
		if err := statusWriter.Setup(); err != nil {
			log.Debug("failed to setup status bar", "error", err)
			statusWriter = nil
		}
	}
}

// Ensure cleanup
if statusWriter != nil {
	defer statusWriter.Cleanup()
}

// Use statusWriter or os.Stdout for attachment
var stdout io.Writer = os.Stdout
if statusWriter != nil {
	stdout = statusWriter
}
```

**Step 2: Run manual test**

Run: `./moat run -d -- sleep 300` (start detached)
Then: `./moat attach -i <run-id>`
Expected: Status bar appears

**Step 3: Commit**

```bash
git add cmd/moat/cli/attach.go
git commit -m "feat(tui): integrate status bar with interactive attach"
```

---

## Task 7: Add SIGWINCH Handler

**Files:**
- Modify: `cmd/moat/cli/exec.go`
- Modify: `cmd/moat/cli/attach.go`

**Step 1: Add resize signal handling**

In both `RunInteractiveAttached` and `attachInteractiveMode`:

```go
// Add syscall.SIGWINCH to signal handling
signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGWINCH)

// In the select loop, add case:
case sig := <-sigCh:
	if sig == syscall.SIGWINCH {
		// Handle resize
		if statusWriter != nil && term.IsTerminal(os.Stdout) {
			width, height := term.GetSize(os.Stdout)
			if width > 0 && height > 0 {
				_ = statusWriter.Resize(width, height)
				// Also resize container TTY
				_ = manager.ResizeTTY(ctx, r.ID, uint(height), uint(width))
			}
		}
		continue // Don't break out of loop
	}
	// ... existing SIGINT/SIGTERM handling
```

**Step 2: Run manual test**

Run: `./moat run -i -- bash`
Then: Resize terminal window
Expected: Status bar redraws at new position

**Step 3: Commit**

```bash
git add cmd/moat/cli/exec.go cmd/moat/cli/attach.go
git commit -m "feat(tui): handle SIGWINCH for terminal resize"
```

---

## Task 8: Final Polish and Tests

**Files:**
- Create: `internal/tui/integration_test.go` (optional)
- Update: Documentation

**Step 1: Run full test suite**

Run: `go test ./...`
Expected: All pass

**Step 2: Run E2E test if available**

Run: `go test -tags=e2e -v ./internal/e2e/`

**Step 3: Manual smoke test**

```bash
# Test 1: Interactive run
./moat run -i -- bash
# Verify: status bar at bottom, run commands, resize terminal

# Test 2: Attach
./moat run -d -- sleep 300
./moat attach -i <run-id>
# Verify: status bar appears

# Test 3: Non-TTY (should NOT show status bar)
./moat run -i -- echo "hello" | cat
# Verify: no escape sequences in output
```

**Step 4: Update design doc with implementation notes**

Add "Implementation Complete" section to the design doc.

**Step 5: Final commit**

```bash
git add .
git commit -m "feat(tui): complete status bar implementation"
```

---

## Summary

| Task | Description | Files |
|------|-------------|-------|
| 1 | StatusBar core | `internal/tui/statusbar.go` |
| 2 | ANSI escapes | `internal/tui/statusbar.go` |
| 3 | Writer wrapper | `internal/tui/writer.go` |
| 4 | Resize support | `internal/tui/writer.go` |
| 5 | exec.go integration | `cmd/moat/cli/exec.go` |
| 6 | attach.go integration | `cmd/moat/cli/attach.go` |
| 7 | SIGWINCH handling | `cmd/moat/cli/exec.go`, `attach.go` |
| 8 | Polish and testing | Various |
