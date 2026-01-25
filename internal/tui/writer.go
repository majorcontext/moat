package tui

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"github.com/charmbracelet/x/vt"
)

// appleContainerReadyMarker is printed by Apple's container CLI when the
// container is attached and ready for input. We use this to detect when
// to clear the startup spinner and initialize the status bar.
const appleContainerReadyMarker = "Escape sequences:"

// maxInitBuffer limits how much data we buffer while waiting for the Apple
// container ready marker. If the marker is never received (e.g., container
// crashes during startup), this prevents unbounded memory growth.
const maxInitBuffer = 64 * 1024 // 64KB

// Writer wraps an io.Writer and composites a status bar at the bottom.
// It uses a virtual terminal emulator to track the screen state, allowing
// perfect rendering on resize without race conditions.
//
// Writer is goroutine-safe for all methods.
type Writer struct {
	mu       sync.Mutex
	out      io.Writer        // Actual terminal output
	emulator *vt.SafeEmulator // Virtual terminal for content area
	bar      *StatusBar
	width    int
	height   int // Total terminal height (content + status bar)

	// Apple container specific
	runtime     string // "apple" or "docker"
	initialized bool   // true once we've cleared and set up the status bar
	buffer      []byte // buffers output until container is ready (Apple only)
}

// NewWriter creates a Writer that composites container output with a status bar.
// The runtime parameter should be "apple" or "docker" to enable runtime-specific
// behavior (e.g., detecting Apple container CLI's ready marker).
func NewWriter(w io.Writer, bar *StatusBar, runtime string) *Writer {
	width := bar.width
	height := bar.height
	contentHeight := max(height-1, 1)

	return &Writer{
		out:      w,
		emulator: vt.NewSafeEmulator(width, contentHeight),
		bar:      bar,
		width:    width,
		height:   height,
		runtime:  runtime,
	}
}

// Setup initializes the terminal for status bar display.
// Call this before any writes to clear the screen and draw initial state.
func (w *Writer) Setup() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Clear screen and home cursor
	_, err := w.out.Write([]byte("\x1b[2J\x1b[H"))
	if err != nil {
		return err
	}

	// Render initial state (empty content area + status bar)
	return w.renderLocked()
}

// Write writes data to the virtual terminal and re-renders the display.
func (w *Writer) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// For Apple containers, detect the ready marker and clear content area
	if w.runtime == "apple" && !w.initialized {
		// Buffer data up to maxInitBuffer to detect the ready marker
		if len(w.buffer) < maxInitBuffer {
			remaining := maxInitBuffer - len(w.buffer)
			if len(p) <= remaining {
				w.buffer = append(w.buffer, p...)
			} else {
				w.buffer = append(w.buffer, p[:remaining]...)
			}
		}

		// Write to virtual terminal (errors indicate invalid ANSI, safe to ignore)
		_, _ = w.emulator.Write(p)

		// Render current state
		_ = w.renderLocked()

		// Check if we've seen the ready marker
		if bytes.Contains(w.buffer, []byte(appleContainerReadyMarker)) {
			w.initialized = true
			// Reset the emulator to clear the spinner
			w.emulator.Resize(w.width, w.height-1)
			_ = w.renderLocked()
			// Clear buffer - no longer needed
			w.buffer = nil
		}
		return len(p), nil
	}

	// Write to virtual terminal (it parses ANSI and updates screen state)
	_, _ = w.emulator.Write(p)

	// Render: content from emulator + status bar
	err = w.renderLocked()
	return len(p), err
}

// Cleanup resets the terminal state.
func (w *Writer) Cleanup() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Clear screen and show cursor
	_, err := w.out.Write([]byte("\x1b[2J\x1b[H\x1b[?25h"))
	return err
}

// Resize updates the terminal dimensions and re-renders.
// This is race-condition free because we re-render from the virtual terminal's
// buffer rather than relying on the physical terminal's state.
func (w *Writer) Resize(width, height int) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.width = width
	w.height = height
	w.bar.SetDimensions(width, height)

	// Resize the virtual terminal's content area (total height minus status bar)
	contentHeight := max(height-1, 1)
	w.emulator.Resize(width, contentHeight)

	// Clear physical screen and re-render everything from our buffer
	_, _ = w.out.Write([]byte("\x1b[2J\x1b[H"))

	return w.renderLocked()
}

// renderLocked renders the virtual terminal content plus status bar.
// Caller must hold the mutex.
func (w *Writer) renderLocked() error {
	// Get cursor position before rendering (0-indexed)
	pos := w.emulator.CursorPosition()

	// Get rendered content from virtual terminal (includes ANSI codes)
	content := w.emulator.Render()

	// Build output: content + status bar at bottom
	var buf bytes.Buffer

	// Position cursor at top-left and write content
	buf.WriteString("\x1b[H")
	buf.WriteString(content)

	// Position cursor at bottom row and write status bar
	// Use w.height (the actual terminal height) for status bar position
	buf.WriteString(w.bar.Render())

	// Restore cursor to its position in the content area
	// ANSI positions are 1-indexed, vt positions are 0-indexed
	fmt.Fprintf(&buf, "\x1b[%d;%dH", pos.Y+1, pos.X+1)

	_, err := w.out.Write(buf.Bytes())
	return err
}
