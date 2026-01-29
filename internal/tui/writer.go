package tui

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

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

// Alternate screen mode escape sequences. We detect these to switch between
// scroll mode (DECSTBM passthrough) and compositor mode (VT emulator).
var altScreenEnter = [][]byte{
	[]byte("\x1b[?1049h"),
	[]byte("\x1b[?47h"),
	[]byte("\x1b[?1047h"),
}

var altScreenExit = [][]byte{
	[]byte("\x1b[?1049l"),
	[]byte("\x1b[?47l"),
	[]byte("\x1b[?1047l"),
}

// renderInterval is the compositor render tick rate (~60fps).
const renderInterval = 16 * time.Millisecond

// Writer wraps an io.Writer and adds a status bar at the bottom using a
// dual-mode approach:
//
// Scroll mode (default): DECSTBM scroll region pins the footer. Output passes
// through to the real terminal so scrollback works with zero overhead.
//
// Compositor mode: activated when the child process enters alternate screen
// mode. Output is fed to a VT emulator and the emulator screen is rendered
// to the real terminal with the footer appended.
//
// Writer is goroutine-safe for all methods.
type Writer struct {
	mu     sync.Mutex
	out    io.Writer // Actual terminal output
	bar    *StatusBar
	width  int
	height int // Total terminal height (content + status bar)

	// Compositor mode state
	altScreen bool
	emulator  *vt.Emulator

	// Escape sequence parser state for detecting alt screen sequences
	// that may be split across Write() calls.
	escBuf []byte

	// Render coalescing for compositor mode
	dirty        bool
	renderTicker *time.Ticker
	stopRender   chan struct{}

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

// Write processes container output, scanning for alternate screen mode
// transitions. In scroll mode, output passes through directly. In compositor
// mode, output is fed to the VT emulator for rendering.
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

	// Prepend any buffered partial escape sequence from previous Write
	var data []byte
	if len(w.escBuf) > 0 {
		data = make([]byte, len(w.escBuf)+len(p))
		copy(data, w.escBuf)
		copy(data[len(w.escBuf):], p)
		w.escBuf = nil
	} else {
		data = p
	}

	// Process data, scanning for alt screen transitions
	err = w.processDataLocked(data)

	// In scroll mode, redraw the footer after every write. The child process
	// can clobber it by resetting the scroll region, clearing the screen, or
	// addressing the cursor to the footer line directly. Redrawing after each
	// write ensures the footer is always visible (same approach as tmux).
	if !w.altScreen && err == nil {
		w.redrawFooterLocked()
	}

	return len(p), err
}

// processDataLocked scans data for alternate screen enter/exit sequences,
// splitting output into segments that are either passed through (scroll mode)
// or fed to the emulator (compositor mode).
func (w *Writer) processDataLocked(data []byte) error {
	for len(data) > 0 {
		// Find the next ESC character
		idx := bytes.IndexByte(data, 0x1b)
		if idx == -1 {
			// No escape sequences - output everything
			return w.outputLocked(data)
		}

		// Output everything before the ESC
		if idx > 0 {
			if err := w.outputLocked(data[:idx]); err != nil {
				return err
			}
			data = data[idx:]
		}

		// Try to match an alt screen sequence at this position
		matched, enter, seqLen := w.matchAltScreen(data)
		if matched {
			// Consume the sequence (don't pass it through)
			data = data[seqLen:]
			if enter {
				if err := w.enterCompositorLocked(); err != nil {
					return err
				}
			} else {
				if err := w.exitCompositorLocked(); err != nil {
					return err
				}
			}
			continue
		}

		// Check if this could be a partial match at the end of the buffer
		if w.isPrefixOfAltScreen(data) && len(data) < maxAltScreenSeqLen() {
			// Buffer it for the next Write call
			w.escBuf = append(w.escBuf[:0], data...)
			return nil
		}

		// Not an alt screen sequence - output the ESC and continue
		if err := w.outputLocked(data[:1]); err != nil {
			return err
		}
		data = data[1:]
	}
	return nil
}

// outputLocked sends data to either the real terminal (scroll mode) or the
// VT emulator (compositor mode).
func (w *Writer) outputLocked(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if w.altScreen {
		_, err := w.emulator.Write(data)
		if err != nil {
			return err
		}
		w.dirty = true
		return nil
	}
	_, err := w.out.Write(data)
	return err
}

// matchAltScreen checks if data starts with an alt screen enter or exit sequence.
// Returns (matched, isEnter, sequenceLength).
func (w *Writer) matchAltScreen(data []byte) (matched bool, enter bool, length int) {
	for _, seq := range altScreenEnter {
		if bytes.HasPrefix(data, seq) {
			return true, true, len(seq)
		}
	}
	for _, seq := range altScreenExit {
		if bytes.HasPrefix(data, seq) {
			return true, false, len(seq)
		}
	}
	return false, false, 0
}

// isPrefixOfAltScreen returns true if data is a prefix of any alt screen sequence.
func (w *Writer) isPrefixOfAltScreen(data []byte) bool {
	for _, seq := range altScreenEnter {
		if len(data) < len(seq) && bytes.HasPrefix(seq, data) {
			return true
		}
	}
	for _, seq := range altScreenExit {
		if len(data) < len(seq) && bytes.HasPrefix(seq, data) {
			return true
		}
	}
	return false
}

// maxAltScreenSeqLen returns the length of the longest alt screen sequence.
func maxAltScreenSeqLen() int {
	max := 0
	for _, seq := range altScreenEnter {
		if len(seq) > max {
			max = len(seq)
		}
	}
	for _, seq := range altScreenExit {
		if len(seq) > max {
			max = len(seq)
		}
	}
	return max
}

// enterCompositorLocked switches from scroll mode to compositor mode.
func (w *Writer) enterCompositorLocked() error {
	if w.altScreen {
		return nil
	}
	w.altScreen = true

	// Initialize emulator with content area dimensions (height - 1 for footer)
	contentHeight := w.height - 1
	if contentHeight < 1 {
		contentHeight = 1
	}
	w.emulator = vt.NewEmulator(w.width, contentHeight)

	// Enter alternate screen on the real terminal.
	// We do NOT set DECSTBM in compositor mode — the emulator handles scrolling
	// internally, and we render its screen with absolute cursor positioning.
	// Using DECSTBM here would cause the rendered content to scroll within the
	// region and clobber the footer.
	var buf bytes.Buffer
	buf.WriteString("\x1b[?1049h")   // Enter alt screen
	buf.WriteString("\x1b[2J\x1b[H") // Clear and home

	// Draw footer at bottom line
	fmt.Fprintf(&buf, "\x1b[%d;1H", w.height)
	buf.WriteString(w.bar.Render())
	buf.WriteString("\x1b[H")

	if _, err := w.out.Write(buf.Bytes()); err != nil {
		// Revert state — no goroutine has started yet.
		w.altScreen = false
		w.emulator = nil
		return err
	}

	// Start render ticker after the write succeeds so there's no
	// goroutine to leak if the write fails.
	w.dirty = false
	w.stopRender = make(chan struct{})
	w.renderTicker = time.NewTicker(renderInterval)
	go w.renderLoop(w.renderTicker, w.stopRender)

	return nil
}

// exitCompositorLocked switches from compositor mode back to scroll mode.
func (w *Writer) exitCompositorLocked() error {
	if !w.altScreen {
		return nil
	}
	w.altScreen = false

	// Stop render loop
	w.stopRenderLoop()

	// Do a final render to flush any pending content
	w.renderCompositorLocked()

	w.emulator = nil

	// Exit alternate screen
	var buf bytes.Buffer
	buf.WriteString("\x1b[?1049l")

	// Re-establish scroll region on the main screen
	if w.height > 1 {
		fmt.Fprintf(&buf, "\x1b[1;%dr", w.height-1)
	}
	// Redraw footer
	fmt.Fprintf(&buf, "\x1b[%d;1H", w.height)
	buf.WriteString(w.bar.Render())
	buf.WriteString("\x1b[H")

	_, err := w.out.Write(buf.Bytes())
	return err
}

// renderLoop runs in a goroutine, checking the dirty flag and rendering
// the compositor at ~60fps.
func (w *Writer) renderLoop(ticker *time.Ticker, stop chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			w.mu.Lock()
			if w.dirty && w.altScreen && w.emulator != nil {
				w.renderCompositorLocked()
				w.dirty = false
			}
			w.mu.Unlock()
		}
	}
}

// stopRenderLoop stops the render ticker and goroutine.
func (w *Writer) stopRenderLoop() {
	if w.renderTicker != nil {
		w.renderTicker.Stop()
		w.renderTicker = nil
	}
	if w.stopRender != nil {
		close(w.stopRender)
		w.stopRender = nil
	}
}

// renderCompositorLocked renders the emulator screen to the real terminal.
// Each row is positioned absolutely to avoid DECSTBM scroll interactions.
// Caller must hold the mutex.
func (w *Writer) renderCompositorLocked() {
	if w.emulator == nil {
		return
	}

	var buf bytes.Buffer

	// Hide cursor during render to avoid flicker
	buf.WriteString("\x1b[?25l")

	// Render emulator content with ANSI styles, then write row-by-row
	// using absolute cursor positioning. This avoids relying on newlines
	// which could cause scrolling and clobber the footer.
	rendered := w.emulator.Render()
	lines := strings.Split(rendered, "\r\n")

	contentHeight := w.height - 1
	for i := 0; i < contentHeight; i++ {
		fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[2K", i+1) // Move to row, clear line
		if i < len(lines) {
			buf.WriteString(lines[i])
		}
	}

	// Redraw footer at bottom
	fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[2K", w.height)
	buf.WriteString(w.bar.Render())

	// Show cursor and position it based on emulator cursor
	buf.WriteString("\x1b[?25h")
	pos := w.emulator.CursorPosition()
	fmt.Fprintf(&buf, "\x1b[%d;%dH", pos.Y+1, pos.X+1)

	w.out.Write(buf.Bytes()) //nolint:errcheck
}

// Cleanup resets the terminal state.
func (w *Writer) Cleanup() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Stop compositor if running
	w.stopRenderLoop()
	w.emulator = nil

	var buf bytes.Buffer

	// Exit alternate screen if we're in one
	if w.altScreen {
		buf.WriteString("\x1b[?1049l")
		w.altScreen = false
	}

	// Reset scrolling region to full screen (DECSTBM with no params)
	buf.WriteString("\x1b[r")

	// Clear screen and show cursor
	buf.WriteString("\x1b[2J\x1b[H\x1b[?25h")

	_, err := w.out.Write(buf.Bytes())
	return err
}

// Resize updates the terminal dimensions and re-establishes the layout.
// This must be called on SIGWINCH to maintain the status bar after terminal resize.
func (w *Writer) Resize(width, height int) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.width = width
	w.height = height
	w.bar.SetDimensions(width, height)

	if w.altScreen && w.emulator != nil {
		// Resize emulator to new content area
		contentHeight := height - 1
		if contentHeight < 1 {
			contentHeight = 1
		}
		w.emulator.Resize(width, contentHeight)
		w.dirty = true

		// Clear and redraw footer (no DECSTBM in compositor mode)
		var buf bytes.Buffer
		buf.WriteString("\x1b[2J\x1b[H")
		fmt.Fprintf(&buf, "\x1b[%d;1H", height)
		buf.WriteString(w.bar.Render())
		buf.WriteString("\x1b[H")
		w.out.Write(buf.Bytes()) //nolint:errcheck
		return nil
	}

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

// redrawFooterLocked redraws the footer at the bottom line without disturbing
// the cursor position. Used in scroll mode to repair the footer after child
// output that may have clobbered it (scroll region reset, screen clear, or
// direct cursor addressing to the footer line).
// Caller must hold the mutex.
func (w *Writer) redrawFooterLocked() {
	var buf bytes.Buffer
	buf.WriteString("\x1b7")                  // DECSC: save cursor + attrs
	fmt.Fprintf(&buf, "\x1b[%d;1H", w.height) // Move to footer line
	buf.WriteString("\x1b[2K")                // Clear the line
	buf.WriteString(w.bar.Render())           // Draw footer
	buf.WriteString("\x1b8")                  // DECRC: restore cursor + attrs
	w.out.Write(buf.Bytes())                  //nolint:errcheck
}
