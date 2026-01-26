package tui

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriter_Write(t *testing.T) {
	var buf bytes.Buffer
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(60, 24)

	w := NewWriter(&buf, bar, "docker")

	n, err := w.Write([]byte("hello world\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 12 {
		t.Errorf("expected n=12, got %d", n)
	}

	output := buf.String()

	// Should contain the original content
	if !strings.Contains(output, "hello world") {
		t.Errorf("expected original content in output")
	}

	// With vt-based approach, status bar IS rendered on every write (compositing)
	if !strings.Contains(output, "run_abc123") {
		t.Errorf("expected status bar in output")
	}
}

func TestWriter_Setup(t *testing.T) {
	var buf bytes.Buffer
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(60, 24)

	w := NewWriter(&buf, bar, "docker")
	err := w.Setup()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Should clear screen first
	if !strings.Contains(output, "\x1b[2J") {
		t.Errorf("expected clear screen in setup, got %q", output)
	}

	// Should home cursor
	if !strings.Contains(output, "\x1b[H") {
		t.Errorf("expected cursor home in setup, got %q", output)
	}

	// Should render status bar
	if !strings.Contains(output, "run_abc123") {
		t.Errorf("expected status bar in setup")
	}
}

func TestWriter_Write_PassesThrough(t *testing.T) {
	var buf bytes.Buffer
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(60, 24)

	w := NewWriter(&buf, bar, "docker")
	_ = w.Setup()
	buf.Reset() // Clear setup output

	// Write should render content + status bar (compositing)
	_, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Should contain the content
	if !strings.Contains(output, "hello") {
		t.Errorf("expected content in output")
	}

	// With vt-based approach, status bar is always composited
	if !strings.Contains(output, "run_abc123") {
		t.Errorf("expected status bar in composited output")
	}
}

func TestWriter_Cleanup(t *testing.T) {
	var buf bytes.Buffer
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(60, 24)

	w := NewWriter(&buf, bar, "docker")
	err := w.Cleanup()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Should clear screen
	if !strings.Contains(output, "\x1b[2J") {
		t.Errorf("expected clear screen in cleanup, got %q", output)
	}

	// Should show cursor
	if !strings.Contains(output, "\x1b[?25h") {
		t.Errorf("expected show cursor in cleanup, got %q", output)
	}
}

func TestWriter_Resize(t *testing.T) {
	var buf bytes.Buffer
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(60, 24)

	w := NewWriter(&buf, bar, "docker")
	w.Setup()
	buf.Reset() // Clear setup output

	// Simulate resize
	err := w.Resize(80, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Should clear screen and re-render
	if !strings.Contains(output, "\x1b[2J") {
		t.Errorf("expected clear screen in resize, got %q", output)
	}

	// Should render status bar at new position
	if !strings.Contains(output, "run_abc123") {
		t.Errorf("expected status bar in resize output")
	}
}

func TestWriter_Resize_Grow(t *testing.T) {
	var buf bytes.Buffer
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(80, 24) // Start at height 24

	w := NewWriter(&buf, bar, "docker")
	w.Setup()
	buf.Reset() // Clear setup output

	// Grow from 24 to 30
	err := w.Resize(80, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Should clear and re-render - this avoids ghost status bars
	if !strings.Contains(output, "\x1b[2J") {
		t.Errorf("expected clear screen on grow, got %q", output)
	}

	// Should draw status bar at new row 30
	if !strings.Contains(output, "\x1b[30;1H") {
		t.Errorf("expected status bar at row 30, got %q", output)
	}
}

func TestWriter_Resize_Shrink(t *testing.T) {
	var buf bytes.Buffer
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(80, 30) // Start at height 30

	w := NewWriter(&buf, bar, "docker")
	w.Setup()
	buf.Reset() // Clear setup output

	// Shrink from 30 to 24
	err := w.Resize(80, 24)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Should clear and re-render
	if !strings.Contains(output, "\x1b[2J") {
		t.Errorf("expected clear screen on shrink, got %q", output)
	}

	// Should draw status bar at row 24
	if !strings.Contains(output, "\x1b[24;1H") {
		t.Errorf("expected status bar at row 24, got %q", output)
	}
}

func TestWriter_Apple_ShowsSpinnerThenClears(t *testing.T) {
	var buf bytes.Buffer
	bar := NewStatusBar("run_abc123", "my-agent", "apple")
	bar.SetDimensions(60, 24)

	w := NewWriter(&buf, bar, "apple")
	_ = w.Setup()

	// Setup should clear screen
	setupOutput := buf.String()
	if !strings.Contains(setupOutput, "\x1b[2J") {
		t.Errorf("expected clear screen in setup, got %q", setupOutput)
	}

	buf.Reset()

	// Write spinner output
	_, err := w.Write([]byte("⠼ Starting container [0s]"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Starting container") {
		t.Errorf("expected spinner to pass through, got %q", output)
	}

	buf.Reset()

	// Write the ready marker - should trigger content area clear
	_, err = w.Write([]byte("\nEscape sequences: Ctrl-/ d (detach)\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output = buf.String()

	// After initialization, emulator is resized which clears previous content
	// and renders fresh. Status bar should be present.
	if !strings.Contains(output, "run_abc123") {
		t.Errorf("expected status bar after ready marker, got %q", output)
	}
}

func TestWriter_Apple_PassthroughAfterReady(t *testing.T) {
	var buf bytes.Buffer
	bar := NewStatusBar("run_abc123", "my-agent", "apple")
	bar.SetDimensions(60, 24)

	w := NewWriter(&buf, bar, "apple")
	_ = w.Setup()

	// Trigger initialization with ready marker
	_, _ = w.Write([]byte("Escape sequences: Ctrl-/ d\n"))
	buf.Reset() // Clear initialization output

	// Subsequent writes should composite content + status bar
	_, err := w.Write([]byte("shell prompt $ "))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Should contain the shell output
	if !strings.Contains(output, "shell prompt") {
		t.Errorf("expected shell output to pass through, got %q", output)
	}

	// Status bar should be composited
	if !strings.Contains(output, "run_abc123") {
		t.Errorf("expected status bar in composited output, got %q", output)
	}
}

func TestWriter_VirtualTerminal_PreservesContent(t *testing.T) {
	var buf bytes.Buffer
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(60, 10)

	w := NewWriter(&buf, bar, "docker")
	_ = w.Setup()

	// Write some content
	_, _ = w.Write([]byte("line 1\n"))
	_, _ = w.Write([]byte("line 2\n"))
	buf.Reset()

	// Resize - content should be preserved from virtual terminal
	err := w.Resize(60, 15)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Virtual terminal preserves content across resize
	if !strings.Contains(output, "line 1") {
		t.Errorf("expected line 1 preserved after resize, got %q", output)
	}
	if !strings.Contains(output, "line 2") {
		t.Errorf("expected line 2 preserved after resize, got %q", output)
	}
}

func TestWriter_Apple_BufferSizeLimit(t *testing.T) {
	var buf bytes.Buffer
	bar := NewStatusBar("run_abc123", "my-agent", "apple")
	bar.SetDimensions(60, 24)

	w := NewWriter(&buf, bar, "apple")
	_ = w.Setup()
	buf.Reset()

	// Write more data than maxInitBuffer without the ready marker
	largeData := make([]byte, maxInitBuffer+1000)
	for i := range largeData {
		largeData[i] = 'x'
	}

	_, err := w.Write(largeData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Buffer should be capped at maxInitBuffer
	w.mu.Lock()
	bufLen := len(w.buffer)
	w.mu.Unlock()

	if bufLen > maxInitBuffer {
		t.Errorf("expected buffer <= %d, got %d", maxInitBuffer, bufLen)
	}
}

func TestWriter_CursorVisibilityTracking(t *testing.T) {
	var buf bytes.Buffer
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(60, 24)

	w := NewWriter(&buf, bar, "docker")
	_ = w.Setup()

	// Initially cursor is visible
	w.mu.Lock()
	if !w.cursorVisible {
		t.Error("expected cursor visible initially")
	}
	w.mu.Unlock()
	buf.Reset()

	// Write with cursor hide sequence
	_, _ = w.Write([]byte("hello\x1b[?25l"))
	w.mu.Lock()
	if w.cursorVisible {
		t.Error("expected cursor hidden after hide sequence")
	}
	w.mu.Unlock()

	// Output should include cursor hide
	if !strings.Contains(buf.String(), "\x1b[?25l") {
		t.Errorf("expected cursor hide in output, got %q", buf.String())
	}
	buf.Reset()

	// Write with cursor show sequence
	_, _ = w.Write([]byte("world\x1b[?25h"))
	w.mu.Lock()
	if !w.cursorVisible {
		t.Error("expected cursor visible after show sequence")
	}
	w.mu.Unlock()

	// Output should include cursor show
	if !strings.Contains(buf.String(), "\x1b[?25h") {
		t.Errorf("expected cursor show in output, got %q", buf.String())
	}
}

func TestWriter_CursorVisibility_LastWins(t *testing.T) {
	var buf bytes.Buffer
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(60, 24)

	w := NewWriter(&buf, bar, "docker")
	_ = w.Setup()
	buf.Reset()

	// Write with both sequences - last one wins
	_, _ = w.Write([]byte("\x1b[?25l\x1b[?25h\x1b[?25l"))
	w.mu.Lock()
	visible := w.cursorVisible
	w.mu.Unlock()

	if visible {
		t.Error("expected cursor hidden (last sequence was hide)")
	}
}
