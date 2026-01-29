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

	// Should contain the original content (passed through directly)
	if !strings.Contains(output, "hello world") {
		t.Errorf("expected original content in output")
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

	// Write should pass through content directly (scrolling region handles layout)
	_, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Should contain the content
	if !strings.Contains(output, "hello") {
		t.Errorf("expected content in output, got %q", output)
	}

	// Status bar is NOT rewritten on every write (only during Setup/Resize)
	// This is the key difference from the VT emulator approach
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

	// Subsequent writes should pass through directly
	_, err := w.Write([]byte("shell prompt $ "))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Should contain the shell output
	if !strings.Contains(output, "shell prompt") {
		t.Errorf("expected shell output to pass through, got %q", output)
	}

	// Status bar is NOT rewritten on every write (scrolling region keeps it pinned)
}

func TestWriter_ScrollingRegion_Resize(t *testing.T) {
	var buf bytes.Buffer
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(60, 10)

	w := NewWriter(&buf, bar, "docker")
	_ = w.Setup()

	// Write some content
	_, _ = w.Write([]byte("line 1\n"))
	_, _ = w.Write([]byte("line 2\n"))
	buf.Reset()

	// Resize re-establishes scrolling region and redraws status bar
	err := w.Resize(60, 15)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Should set DECSTBM scrolling region for new height (1-14)
	if !strings.Contains(output, "\x1b[1;14r") {
		t.Errorf("expected DECSTBM for height 15, got %q", output)
	}

	// Should redraw status bar
	if !strings.Contains(output, "run_abc123") {
		t.Errorf("expected status bar after resize, got %q", output)
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

func TestWriter_PassthroughANSI(t *testing.T) {
	var buf bytes.Buffer
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(60, 24)

	w := NewWriter(&buf, bar, "docker")
	_ = w.Setup()
	buf.Reset()

	// ANSI sequences should pass through unchanged
	_, _ = w.Write([]byte("hello\x1b[?25l"))

	// Output should include the cursor hide sequence
	if !strings.Contains(buf.String(), "\x1b[?25l") {
		t.Errorf("expected cursor hide in output, got %q", buf.String())
	}

	// Output should include the content
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("expected content in output, got %q", buf.String())
	}
}
