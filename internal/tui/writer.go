package tui

import (
	"bytes"
	"fmt"
	"io"
	"sync"
)

// appleContainerReadyMarker is printed by Apple's container CLI when the
// container is attached and ready for input. We use this to detect when
// to clear the startup spinner and initialize the status bar.
const appleContainerReadyMarker = "Escape sequences:"

// maxInitBuffer limits how much data we buffer while waiting for the Apple
// container ready marker. If the marker is never received (e.g., container
// crashes during startup), this prevents unbounded memory growth.
const maxInitBuffer = 64 * 1024 // 64KB

// Writer wraps an io.Writer and adds a status bar at the bottom using DECSTBM
// (scrolling regions). Container output scrolls naturally in the top region
// while the status bar remains pinned at the bottom.
//
// Writer is goroutine-safe for all methods.
type Writer struct {
	mu     sync.Mutex
	out    io.Writer // Actual terminal output
	bar    *StatusBar
	width  int
	height int // Total terminal height (content + status bar)

	// Apple container specific
	runtime     string // "apple" or "docker"
	initialized bool   // true once we've cleared and set up the status bar
	buffer      []byte // buffers output until container is ready (Apple only)
}

// NewWriter creates a Writer that composites container output with a status bar.
// The runtime parameter should be "apple" or "docker" to enable runtime-specific
// behavior (e.g., detecting Apple container CLI's ready marker).
func NewWriter(w io.Writer, bar *StatusBar, runtime string) *Writer {
	return &Writer{
		out:     w,
		bar:     bar,
		width:   bar.width,
		height:  bar.height,
		runtime: runtime,
	}
}

// Setup initializes the terminal for status bar display using scrolling regions.
// Sets DECSTBM to create a scrolling region for content (lines 1 to height-1)
// and pins the status bar at the bottom line.
func (w *Writer) Setup() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.setupScrollRegionLocked()
}

// setupScrollRegionLocked sets up the scrolling region and draws the status bar.
// Caller must hold the mutex.
func (w *Writer) setupScrollRegionLocked() error {
	var buf bytes.Buffer

	// Clear screen
	buf.WriteString("\x1b[2J\x1b[H")

	// Set scrolling region to lines 1 through height-1
	// DECSTBM: CSI top;bottom r
	if w.height > 1 {
		fmt.Fprintf(&buf, "\x1b[1;%dr", w.height-1)
	}

	// Draw status bar at bottom line (outside scroll region)
	fmt.Fprintf(&buf, "\x1b[%d;1H", w.height)
	buf.WriteString(w.bar.Render())

	// Move cursor to top of scroll region
	buf.WriteString("\x1b[H")

	_, err := w.out.Write(buf.Bytes())
	return err
}

// Write passes container output directly to the terminal.
// The scrolling region ensures output scrolls naturally while the status bar
// remains pinned at the bottom.
func (w *Writer) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// For Apple containers, detect the ready marker and clear screen
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

		// Pass through the output
		_, err = w.out.Write(p)

		// Check if we've seen the ready marker
		if bytes.Contains(w.buffer, []byte(appleContainerReadyMarker)) {
			w.initialized = true
			// Clear screen and re-setup scroll region to clear the spinner
			_ = w.setupScrollRegionLocked()
			// Clear buffer - no longer needed
			w.buffer = nil
		}
		return len(p), err
	}

	// Pass through container output directly - scrolling region handles layout
	return w.out.Write(p)
}

// Cleanup resets the terminal state.
func (w *Writer) Cleanup() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	var buf bytes.Buffer

	// Reset scrolling region to full screen (DECSTBM with no params)
	buf.WriteString("\x1b[r")

	// Clear screen and show cursor
	buf.WriteString("\x1b[2J\x1b[H\x1b[?25h")

	_, err := w.out.Write(buf.Bytes())
	return err
}

// Resize updates the terminal dimensions and re-establishes the scrolling region.
// This must be called on SIGWINCH to maintain the status bar after terminal resize.
func (w *Writer) Resize(width, height int) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.width = width
	w.height = height
	w.bar.SetDimensions(width, height)

	// Re-establish scrolling region and redraw status bar
	return w.setupScrollRegionLocked()
}

// UpdateStatus updates the status bar content.
// This is safe to call while the container is running.
func (w *Writer) UpdateStatus() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	var buf bytes.Buffer

	// Save cursor position
	buf.WriteString("\x1b[s")

	// Move to status bar line and draw it
	fmt.Fprintf(&buf, "\x1b[%d;1H", w.height)
	buf.WriteString(w.bar.Render())

	// Restore cursor position
	buf.WriteString("\x1b[u")

	_, err := w.out.Write(buf.Bytes())
	return err
}
