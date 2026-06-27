package tui

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/term"
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
// Injector forwards bytes the VT emulator generates as replies (e.g.,
// Primary Device Attributes for CSI c, cursor-position reports for CSI 6n,
// in-band resize notifications) back into the child's input stream so the
// child sees the responses it queried for.
//
// Compositor mode requires an injector to be wired up via SetInjector; the
// emulator's reply handlers write into an internal io.Pipe and the reader
// side MUST be drained or the next reply-bearing handler deadlocks while
// holding the Writer's mutex, freezing the screen.
type Injector interface {
	Inject(b []byte) error
}

// Writer is goroutine-safe for all methods.
type Writer struct {
	mu     sync.Mutex
	out    io.Writer // Actual terminal output
	bar    *StatusBar
	width  int
	height int // Total terminal height (content + status bar)

	// injector receives bytes the emulator generates as replies to child
	// queries (Primary DA, cursor position, etc.). May be nil — in that case
	// emulator replies are still drained (to prevent deadlock) but discarded.
	// Protected by mu only for the SetInjector path; the drain goroutine
	// captures the value at compositor-entry time.
	injector Injector

	// Compositor mode state
	altScreen bool
	emulator  *vt.Emulator

	// hostMouseModes records the mouse-tracking DEC private modes that moat has
	// enabled on the host terminal on the child's behalf while in compositor
	// mode (see forwardMouseModeLocked). In compositor mode the child's output
	// is consumed by the emulator, so its mouse-mode sequences would never
	// reach the host unless forwarded. We track what we enabled so we can
	// disable it on teardown if the child leaves the alternate screen — or
	// dies — without disabling it itself, which would otherwise leave the
	// user's terminal stuck reporting mouse events.
	hostMouseModes map[int]bool

	// cleanedUp is set once Cleanup() has torn down the status bar. Guards
	// late repaints (e.g. a footer-count poll firing RefreshFooter after exit)
	// from painting a stray status line over the reset screen.
	cleanedUp bool

	// Escape sequence parser state for detecting alt screen sequences
	// that may be split across Write() calls.
	escBuf []byte

	// Render coalescing for compositor mode
	dirty        bool
	renderTicker *time.Ticker
	stopRender   chan struct{}

	// Footer redraw debouncing for scroll mode
	// Redraws the footer only after a quiet period to avoid interrupting
	// multi-step rendering sequences from the child process.
	// Alternative approaches to consider:
	//   (2) Bracketed paste mode detection: only redraw when ESC[?2026l seen
	//   (3) Whitelist: only redraw on resize, screen clear, or periodic timer
	footerDebounceDelay time.Duration
	footerTimer         *time.Timer

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
		out:                 w,
		bar:                 bar,
		width:               bar.width,
		height:              bar.height,
		runtime:             runtime,
		footerDebounceDelay: 50 * time.Millisecond, // Wait 50ms of quiet before redrawing footer
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

	// Set scrolling region to lines 1 through height-1
	// DECSTBM: CSI top;bottom r
	if w.height > 1 {
		fmt.Fprintf(&buf, "\x1b[1;%dr", w.height-1)
	}

	// Draw status bar at bottom line (outside scroll region)
	fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[2K", w.height)
	buf.WriteString(w.bar.Render())

	// Move cursor to top of scroll region and clear from cursor to end of scroll region
	// This clears lines 1 through height-1 without affecting the status bar
	buf.WriteString("\x1b[H\x1b[J")

	_, err := w.out.Write(buf.Bytes())
	return err
}

// Write processes container output, scanning for alternate screen mode
// transitions. In scroll mode, output passes through directly. In compositor
// mode, output is fed to the VT emulator for rendering.
func (w *Writer) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// For Apple containers, buffer data to detect the ready marker.
	// When found, the screen is cleared to remove the startup spinner.
	// Data processing (alt screen detection, footer redraws) always runs
	// below regardless of init state — this ensures the footer works even
	// if the marker is delayed or never arrives.
	if w.runtime == "apple" && !w.initialized {
		if len(w.buffer) < maxInitBuffer {
			remaining := maxInitBuffer - len(w.buffer)
			if len(p) <= remaining {
				w.buffer = append(w.buffer, p...)
			} else {
				w.buffer = append(w.buffer, p[:remaining]...)
			}
		}
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

	// For Apple containers, check if we've seen the ready marker.
	// This runs after processDataLocked so the startup output is written
	// to the terminal before the screen is cleared.
	if w.runtime == "apple" && !w.initialized {
		if bytes.Contains(w.buffer, []byte(appleContainerReadyMarker)) {
			w.initialized = true
			// Clear screen and re-setup scroll region to remove the spinner.
			// Skip if in alt screen since the spinner is on the main screen.
			if !w.altScreen {
				_ = w.setupScrollRegionLocked()
			}
			w.buffer = nil
		} else if len(w.buffer) >= maxInitBuffer {
			// Buffer full without marker — give up waiting.
			// The footer still works because processDataLocked and
			// scheduleFooterRedrawLocked run on every Write regardless.
			w.initialized = true
			w.buffer = nil
		}
	}

	// In scroll mode, schedule a debounced footer redraw. The child process
	// can clobber the footer by resetting the scroll region, clearing the screen,
	// or addressing the cursor to the footer line directly. We use debouncing to
	// avoid interrupting multi-step rendering sequences (e.g., Claude Code's banner
	// draws wrapped in ESC[?2026h/l brackets). We redraw only after output has
	// been quiet for footerDebounceDelay milliseconds.
	if !w.altScreen && err == nil {
		w.scheduleFooterRedrawLocked()
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

		// Check if this could be a partial match at the end of the buffer.
		// This runs before the DECSTBM/DECSTR matcher below; both paths
		// share w.escBuf, but the priority ordering is safe: a 2-byte
		// ESC[ prefix matches alt-screen first and gets buffered, then on
		// the next Write the combined buffer is re-classified by both
		// matchers in turn — if it turns out to be DECSTBM, the second
		// pass routes it correctly.
		if w.isPrefixOfAltScreen(data) && len(data) < maxAltScreenSeqLen {
			// Buffer it for the next Write call
			w.escBuf = append(w.escBuf[:0], data...)
			return nil
		}

		// In scroll mode, intercept terminal-state escapes that would
		// clobber moat's scroll region (DECSTBM, DECSTR, RIS). The
		// emulator owns its own scroll region in compositor mode, so we
		// don't intercept there.
		if !w.altScreen {
			res := matchControlSeq(data)
			if res.needsMore && len(data) <= maxControlSeqBufLen {
				w.escBuf = append(w.escBuf[:0], data...)
				return nil
			}
			if res.kind != ctrlNone {
				if err := w.handleControlSeqLocked(res, data[:res.length]); err != nil {
					return err
				}
				data = data[res.length:]
				continue
			}
			// If needsMore but data exceeded maxControlSeqBufLen, we fall
			// through and emit the ESC byte. The remaining bytes pass to
			// the terminal in order, which reassembles the original
			// CSI — interception silently fails, but real DECSTBMs are
			// well under 10 bytes, so this only fires for pathological
			// input. Memory bound > correctness coverage here.
		} else {
			// In compositor mode the child's output is fed to the emulator,
			// so its mouse-mode set/reset sequences would never reach the host.
			// Those modes control host-global mouse reporting, so forward them
			// to the host instead of swallowing them. This is what stops the
			// wheel from being hijacked: when the child disables mouse on
			// leaving a fullscreen view, the disable reaches the host and
			// reporting turns back off so the wheel scrolls scrollback again.
			matched, set, modes, length, needsMore := matchMouseMode(data)
			if needsMore && len(data) <= maxControlSeqBufLen {
				w.escBuf = append(w.escBuf[:0], data...)
				return nil
			}
			if matched {
				if err := w.forwardMouseModeLocked(set, modes); err != nil {
					return err
				}
				data = data[length:]
				continue
			}
		}

		// Not an alt screen sequence - output the ESC and continue
		if err := w.outputLocked(data[:1]); err != nil {
			return err
		}
		data = data[1:]
	}
	return nil
}

// maxControlSeqBufLen bounds how much of a partial DECSTBM/DECSTR sequence
// we'll buffer before giving up and passing the bytes through. Realistic
// DECSTBMs are 3–8 bytes; the generous cap leaves room for the pathological
// case of many-paramed sequences split across Write boundaries while
// preventing a malformed never-terminating sequence from pinning memory.
const maxControlSeqBufLen = 256

// Assumes 7-bit ANSI input (ESC [, not the 8-bit C1 byte 0x9B). All real
// children writing through this Writer emit UTF-8, where 0x9B can only
// appear as a continuation byte. If we ever support a non-UTF-8 child
// encoding, matchControlSeq will need to grow a C1 branch.

// controlSeqKind tags terminal-state sequences that affect the scroll region.
// In scroll mode, moat owns the scroll region; the child can't be allowed to
// change it directly.
type controlSeqKind int

const (
	ctrlNone    controlSeqKind = iota
	ctrlDECSTBM                // CSI Pt;Pb r — set scroll region. Swallow and re-emit moat's region.
	ctrlDECSTR                 // CSI ! p — soft terminal reset; clears DECSTBM as a side effect. Pass through, then re-emit.
	ctrlRIS                    // ESC c — hard reset; clears screen and DECSTBM. Pass through, then re-establish layout.
)

// controlSeqResult is the outcome of matchControlSeq.
type controlSeqResult struct {
	kind      controlSeqKind
	length    int  // bytes consumed; 0 when no match or partial
	needsMore bool // true if data is a viable prefix of DECSTBM/DECSTR and more data may complete it
}

// matchControlSeq checks whether data starts with a DECSTBM, DECSTR, or RIS
// sequence. Returns needsMore=true if the buffer holds a prefix that could
// still resolve into one of these; caller should buffer and retry.
//
// CSI sequences that share the same final byte but have different syntax
// (e.g. CSI ? 2026 r, a DEC private mode restore) are explicitly NOT matched
// — they pass through to the terminal unmodified.
func matchControlSeq(data []byte) controlSeqResult {
	if len(data) == 0 || data[0] != 0x1b {
		return controlSeqResult{}
	}
	if len(data) == 1 {
		// Bare ESC at end of buffer — anything could follow.
		return controlSeqResult{needsMore: true}
	}
	// RIS: ESC c
	if data[1] == 'c' {
		return controlSeqResult{kind: ctrlRIS, length: 2}
	}
	// Everything else we care about starts with CSI (ESC [).
	if data[1] != '[' {
		return controlSeqResult{}
	}

	i := 2
	paramStart := i
	onlyDigitsAndSemi := true
	for i < len(data) && data[i] >= 0x30 && data[i] <= 0x3F {
		b := data[i]
		if !((b >= '0' && b <= '9') || b == ';') {
			onlyDigitsAndSemi = false
		}
		i++
	}
	paramLen := i - paramStart

	intStart := i
	var firstIntermediate byte
	for i < len(data) && data[i] >= 0x20 && data[i] <= 0x2F {
		if i == intStart {
			firstIntermediate = data[i]
		}
		i++
	}
	intLen := i - intStart

	if i >= len(data) {
		// Incomplete CSI. Could it still be DECSTBM or DECSTR?
		if intLen == 0 && onlyDigitsAndSemi {
			return controlSeqResult{needsMore: true} // could be DECSTBM
		}
		if paramLen == 0 && intLen == 1 && firstIntermediate == '!' {
			return controlSeqResult{needsMore: true} // could be DECSTR
		}
		return controlSeqResult{}
	}

	final := data[i]
	if final < 0x40 || final > 0x7E {
		// Not a valid CSI final byte — let the original parser handle it.
		return controlSeqResult{}
	}
	length := i + 1

	// DECSTBM: digit/semi params (or none), no intermediates, final 'r'.
	if final == 'r' && intLen == 0 && onlyDigitsAndSemi {
		return controlSeqResult{kind: ctrlDECSTBM, length: length}
	}
	// DECSTR: no params, single '!' intermediate, final 'p'.
	if final == 'p' && paramLen == 0 && intLen == 1 && firstIntermediate == '!' {
		return controlSeqResult{kind: ctrlDECSTR, length: length}
	}
	return controlSeqResult{}
}

// handleControlSeqLocked applies the policy for a matched DECSTBM/DECSTR/RIS:
//   - DECSTBM (any args from the child) is swallowed; moat's own scroll
//     region command is emitted in its place.
//   - DECSTR passes through (other resets may be intended), and moat's
//     DECSTBM is re-asserted right after so the footer slot stays reserved.
//     The pair is wrapped in DECSC/DECRC so the cursor — which DECSTR
//     preserves but DECSTBM moves — is restored.
//   - RIS passes through (it clears the screen and homes the cursor), then
//     moat re-establishes its scroll region and footer and returns the
//     cursor to home so the child can resume drawing.
//
// Caller passes raw bytes of the matched sequence so RIS/DECSTR can be
// forwarded verbatim.
func (w *Writer) handleControlSeqLocked(res controlSeqResult, raw []byte) error {
	//nolint:exhaustive // ctrlNone is gated out by the caller (only called when res.kind != ctrlNone)
	switch res.kind {
	case ctrlDECSTBM:
		return w.outputLocked(w.scrollRegionBytes())
	case ctrlDECSTR:
		// Deviation: DECSTR resets the DECSC slot to home (1,1). Our
		// DECSC here overwrites that with the live cursor instead. No
		// known TUI relies on DECSTR's saved-cursor reset; we accept the
		// trade to keep the visible cursor stable across the re-emit.
		//
		// We intentionally do NOT redraw the footer here. DECSTR
		// preserves on-screen content (only modes/state are reset), so
		// the existing footer pixels remain. The debounced redraw fires
		// shortly after this Write returns and repairs the row in case
		// the child writes content immediately afterward. Inline redraw
		// would risk clobbering the child's mid-frame rendering.
		var buf bytes.Buffer
		buf.Write(raw)
		buf.WriteString("\x1b7") // save cursor (DECSTR preserves it)
		buf.Write(w.scrollRegionBytes())
		buf.WriteString("\x1b8") // restore cursor (DECSTBM moves it)
		return w.outputLocked(buf.Bytes())
	case ctrlRIS:
		// Unlike DECSTR, RIS clears the screen — the footer pixels are
		// gone. Redraw it inline rather than waiting for the debounce so
		// the row isn't visibly blank in the gap.
		var buf bytes.Buffer
		buf.Write(raw)
		buf.Write(w.scrollRegionBytes())
		fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[2K", w.height)
		buf.WriteString(w.bar.Render())
		// Return cursor to home so child resumes drawing where RIS left it.
		buf.WriteString("\x1b[H")
		return w.outputLocked(buf.Bytes())
	}
	// Unreachable: callers gate this on res.kind != ctrlNone.
	return nil
}

// mouseModeSet is the set of DEC private modes that control host-global mouse
// reporting. In compositor mode these are forwarded to the host rather than
// fed to the emulator (see processDataLocked / forwardMouseModeLocked), because
// the host — not the emulator — is what reports mouse events back to the child.
//
//	1000 X11 button press/release   1006 SGR mouse encoding
//	1002 button-event (drag)        1007 alternate scroll (wheel→arrows)
//	1003 any-event (motion)         1015 urxvt mouse encoding
//	1005 UTF-8 mouse encoding        1016 SGR-pixels mouse encoding
//
// Non-mouse private modes (cursor visibility, bracketed paste, focus reporting)
// are deliberately excluded: they belong to the emulator/render loop, not the
// host.
var mouseModeSet = map[int]bool{
	1000: true,
	1002: true,
	1003: true,
	1005: true,
	1006: true,
	1007: true,
	1015: true,
	1016: true,
}

// matchMouseMode reports whether data begins with a DEC private mode set/reset
// (CSI ? Pm;... h|l) containing at least one mouse mode. It returns the
// recognized mouse params (so a clean mouse-only sequence can be rebuilt for the
// host), whether it is a set ('h') or reset ('l'), and the bytes consumed.
//
// needsMore is true when data holds an incomplete CSI ? sequence that could
// still resolve into one; the caller should buffer and retry. Sequences with a
// final byte other than h/l (queries, DECRQM, etc.) and private modes with no
// recognized mouse param return the zero value so they flow to the emulator.
func matchMouseMode(data []byte) (matched, set bool, modes []int, length int, needsMore bool) {
	if len(data) == 0 || data[0] != 0x1b {
		return false, false, nil, 0, false
	}
	// "ESC" or "ESC[" could still grow into "ESC[?...".
	if len(data) < 3 {
		if len(data) == 1 || data[1] == '[' {
			return false, false, nil, 0, true
		}
		return false, false, nil, 0, false
	}
	if data[1] != '[' || data[2] != '?' {
		return false, false, nil, 0, false
	}

	i := 3
	for i < len(data) && ((data[i] >= '0' && data[i] <= '9') || data[i] == ';') {
		i++
	}
	if i >= len(data) {
		// Parameters so far but no final byte yet — may still complete.
		return false, false, nil, 0, true
	}
	final := data[i]
	if final != 'h' && final != 'l' {
		return false, false, nil, 0, false
	}

	for _, field := range strings.Split(string(data[3:i]), ";") {
		if field == "" {
			continue
		}
		n, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		if mouseModeSet[n] {
			modes = append(modes, n)
		}
	}
	if len(modes) == 0 {
		return false, false, nil, 0, false
	}
	return true, final == 'h', modes, i + 1, false
}

// forwardMouseModeLocked writes a reconstructed mouse-only set/reset to the host
// and records which modes moat has enabled there. Rebuilding from only the
// recognized mouse params keeps any non-mouse mode that shared the original
// sequence from leaking to the host (the render loop owns host cursor state).
// Caller must hold the mutex.
func (w *Writer) forwardMouseModeLocked(set bool, modes []int) error {
	var b strings.Builder
	b.WriteString("\x1b[?")
	for i, m := range modes {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(strconv.Itoa(m))
	}
	if set {
		b.WriteByte('h')
	} else {
		b.WriteByte('l')
	}
	if _, err := w.out.Write([]byte(b.String())); err != nil {
		return err
	}

	for _, m := range modes {
		if set {
			if w.hostMouseModes == nil {
				w.hostMouseModes = make(map[int]bool)
			}
			w.hostMouseModes[m] = true
		} else {
			delete(w.hostMouseModes, m)
		}
	}
	return nil
}

// disableHostMouseModesLocked returns the bytes that disable every mouse mode
// moat enabled on the host (ascending order, for deterministic output) and
// clears the tracking set. Returns nil when none are active. Caller must hold
// the mutex.
func (w *Writer) disableHostMouseModesLocked() []byte {
	if len(w.hostMouseModes) == 0 {
		return nil
	}
	modes := make([]int, 0, len(w.hostMouseModes))
	for m := range w.hostMouseModes {
		modes = append(modes, m)
	}
	sort.Ints(modes)
	var b strings.Builder
	for _, m := range modes {
		b.WriteString("\x1b[?")
		b.WriteString(strconv.Itoa(m))
		b.WriteByte('l')
	}
	w.hostMouseModes = nil
	return []byte(b.String())
}

// scrollRegionBytes returns the DECSTBM command that pins moat's footer at
// the bottom row. Empty when the terminal is too short to have a region.
func (w *Writer) scrollRegionBytes() []byte {
	if w.height <= 1 {
		return nil
	}
	return []byte(fmt.Sprintf("\x1b[1;%dr", w.height-1))
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

// maxAltScreenSeqLen is the length of the longest alt-screen sequence, derived
// once at init from altScreenEnter/altScreenExit rather than recomputed on every
// Write that hits a partial-escape boundary.
var maxAltScreenSeqLen = func() int {
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
}()

// enterCompositorLocked switches from scroll mode to compositor mode.
func (w *Writer) enterCompositorLocked() error {
	if w.altScreen {
		return nil
	}
	w.altScreen = true

	// Stop footer debounce timer since we're switching to compositor mode
	// where footer is redrawn at fixed intervals instead
	if w.footerTimer != nil {
		w.footerTimer.Stop()
		w.footerTimer = nil
	}

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
	fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[2K", w.height)
	buf.WriteString(w.bar.Render())
	buf.WriteString("\x1b[H")

	if _, err := w.out.Write(buf.Bytes()); err != nil {
		// Revert state — no goroutine has started yet.
		w.altScreen = false
		w.emulator = nil
		return err
	}

	// Drain the emulator's reply pipe and forward replies (Primary DA, cursor
	// position, in-band resize, etc.) to the injector. The emulator's reply
	// handlers write into an internal io.Pipe; without a reader, the next
	// reply-bearing handler blocks while holding w.mu, freezing renderLoop.
	// Closing the emulator's input pipe on exit (via closeEmulatorReplyPipe,
	// called from exitCompositorLocked / Reset / Cleanup) causes
	// emulator.Read to return EOF so this goroutine exits.
	go w.drainEmulatorReplies(w.emulator, w.injector)

	// Start render ticker after the write succeeds so there's no
	// goroutine to leak if the write fails.
	w.dirty = false
	w.stopRender = make(chan struct{})
	w.renderTicker = time.NewTicker(renderInterval)
	go w.renderLoop(w.renderTicker, w.stopRender)

	return nil
}

// SetInjector wires up the destination for VT-emulator-generated replies
// (Primary DA, cursor-position reports, in-band resize notifications, etc.).
//
// Emulator replies are ALWAYS drained from the emulator regardless of
// whether an injector is set — that's a strict invariant of compositor
// mode, otherwise the next reply-bearing handler deadlocks. If no injector
// is set, drained bytes are discarded, the child's capability queries go
// unanswered, and the child falls back to defaults.
//
// The drain goroutine captures the injector at compositor-entry time, so
// SetInjector must be called BEFORE the first byte of container output
// reaches Write to take effect for the first compositor session. Calls
// after entry don't affect the in-flight session.
func (w *Writer) SetInjector(injector Injector) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.injector = injector
}

// emulatorReplyQueueDepth is the buffer size of the channel between the
// emulator-drain goroutine and the injector-forwarder goroutine. It exists
// solely to decouple emulator pipe drainage from injector liveness — see
// drainEmulatorReplies for the rationale. A depth of 16 comfortably absorbs
// a TUI's typical startup query burst (Primary DA, Secondary DA, cursor
// position, color queries, in-band resize) without dropping; sustained
// back-pressure beyond that drops replies, and that is the correct
// behavior (the alternative is deadlock).
const emulatorReplyQueueDepth = 16

// drainEmulatorReplies copies bytes from the emulator's internal reply
// pipe and forwards them to the injector via a bounded buffered channel.
// The decoupling is load-bearing: Inject ultimately writes into an io.Pipe
// that's drained by Docker's stdin-copy goroutine, which back-pressures if
// the container's stdin reader stalls. If drain called Inject directly,
// a stalled container could stall the drain, which would let the next
// reply-bearing emulator handler block inside outputLocked under w.mu —
// the exact deadlock this PR fixes, just one indirection deeper. The
// channel guarantees the emulator pipe is always drained promptly; under
// sustained injector back-pressure we drop replies on a full channel.
//
// Runs as a goroutine while compositor mode is active. Exits when the
// emulator's input pipe is closed (Read returns io.EOF or io.ErrClosedPipe),
// at which point it closes the queue so the forwarder goroutine exits too.
//
// We never call vt.Emulator.Close() here; it writes e.closed concurrently
// with Read's check of that same field (a thread-safety bug in the vt
// package). Instead, closing the underlying io.PipeWriter — which is
// documented as safe for parallel close-vs-read — unblocks Read cleanly.
func (w *Writer) drainEmulatorReplies(em *vt.Emulator, inj Injector) {
	if em == nil {
		return
	}

	queue := make(chan []byte, emulatorReplyQueueDepth)
	forwarderDone := make(chan struct{})
	go func() {
		defer close(forwarderDone)
		for b := range queue {
			if inj == nil {
				continue
			}
			// Best-effort: if Inject fails (e.g., injectable closed) we
			// keep draining the queue so this goroutine exits cleanly
			// when the producer closes it.
			_ = inj.Inject(b)
		}
	}()

	buf := make([]byte, 256)
	for {
		n, err := em.Read(buf)
		if n > 0 {
			// Copy: buf is reused on the next Read and the channel may
			// hand the slice to a slow consumer.
			b := make([]byte, n)
			copy(b, buf[:n])
			select {
			case queue <- b:
			default:
				log.Debug("tui: emulator reply dropped (forwarder back-pressured)",
					"bytes", n, "queue_depth", emulatorReplyQueueDepth)
			}
		}
		if err != nil {
			close(queue)
			<-forwarderDone
			return
		}
	}
}

// closeEmulatorReplyPipe closes the emulator's input pipe writer to signal
// the drain goroutine to exit. We close the pipe directly rather than
// calling vt.Emulator.Close() to avoid a thread-safety bug in the vt
// package where Close writes e.closed while Read concurrently reads it.
// io.Pipe documents Close-vs-Read as safe.
//
// If a future vt release changes InputPipe to return something that isn't
// an io.Closer, we log a warning so the API drift surfaces at runtime.
// The fallout is per-session leakage: the in-flight drain goroutine
// stays parked on em.Read forever (no further writes arrive once we
// orphan the emulator) and the emulator object can't be GC'd. New
// compositor sessions still work correctly — each creates a fresh
// emulator+drain pair — so the symptom is a slowly growing goroutine
// count, not a recurrence of the original screen freeze.
func closeEmulatorReplyPipe(em *vt.Emulator) {
	if em == nil {
		return
	}
	pw, ok := em.InputPipe().(io.Closer)
	if !ok {
		log.Warn("tui: vt.Emulator.InputPipe() no longer satisfies io.Closer; drain goroutine and emulator object will leak once per compositor session")
		return
	}
	_ = pw.Close()
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

	// Close the emulator's reply pipe so the drain goroutine started in
	// enterCompositorLocked exits. See closeEmulatorReplyPipe for why we
	// don't call w.emulator.Close().
	closeEmulatorReplyPipe(w.emulator)
	w.emulator = nil

	// Exit alternate screen
	var buf bytes.Buffer
	buf.WriteString("\x1b[?1049l")

	// Disable any mouse modes moat enabled on the host during compositor mode
	// that the child did not disable itself. Scroll mode passes the child's
	// sequences through directly, so the host should start clean; the child
	// re-enables if it still wants mouse reporting.
	if dis := w.disableHostMouseModesLocked(); dis != nil {
		buf.Write(dis)
	}

	// Re-establish scroll region on the main screen
	if w.height > 1 {
		fmt.Fprintf(&buf, "\x1b[1;%dr", w.height-1)
	}
	// Redraw footer
	fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[2K", w.height)
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

// stopRenderLoop stops the render ticker and goroutine. Safe to call
// multiple times — guards against double-close if Cleanup races with
// exitCompositorLocked (e.g., process kill during alt screen).
func (w *Writer) stopRenderLoop() {
	if w.renderTicker != nil {
		w.renderTicker.Stop()
		w.renderTicker = nil
	}
	if w.stopRender != nil {
		select {
		case <-w.stopRender:
			// Already closed
		default:
			close(w.stopRender)
		}
		w.stopRender = nil
	}
}

// renderCompositorLocked renders the emulator screen to the real terminal.
// Each row is positioned absolutely to avoid DECSTBM scroll interactions.
// Caller must hold the mutex.
func (w *Writer) renderCompositorLocked() {
	// Defensive: the render loop goroutine may fire between
	// exitCompositorLocked setting altScreen=false and clearing the emulator.
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

// Reset attempts to recover the terminal from a corrupted state. It exits
// alternate screen mode if active, drops the VT emulator, emits a soft
// terminal reset (DECSTR), clears the screen, and re-establishes the scroll
// region and footer.
//
// Soft reset (ESC[!p) is used rather than full RIS (ESC c) so the user's
// scrollback is preserved. The caller is responsible for nudging the child
// process to redraw (typically via a no-op TTY resize).
//
// Reset is best-effort. If the terminal write fails partway through, internal
// state (altScreen, emulator, footerTimer, escBuf) has already been cleared,
// so the Writer is left in a valid scroll-mode initial state from which the
// caller may retry. The scroll region/footer redraw may not have completed.
func (w *Writer) Reset() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.footerTimer != nil {
		w.footerTimer.Stop()
		w.footerTimer = nil
	}

	var buf bytes.Buffer

	if w.altScreen {
		w.stopRenderLoop()
		closeEmulatorReplyPipe(w.emulator)
		w.emulator = nil
		w.altScreen = false
		buf.WriteString("\x1b[?1049l")
	}

	// Disable any mouse modes moat mirrored onto the host so a reset leaves the
	// terminal in a clean state.
	if dis := w.disableHostMouseModesLocked(); dis != nil {
		buf.Write(dis)
	}

	// Discard any partial alt-screen escape sequence buffered from a previous
	// Write — carrying it forward could re-trigger a phantom mode transition.
	w.escBuf = nil

	buf.WriteString("\x1b[!p")       // DECSTR soft reset
	buf.WriteString("\x1b[2J\x1b[H") // clear and home
	buf.WriteString("\x1b[?25h")     // show cursor

	if _, err := w.out.Write(buf.Bytes()); err != nil {
		return err
	}

	return w.setupScrollRegionLocked()
}

// Cleanup resets the terminal state.
func (w *Writer) Cleanup() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.cleanedUp = true

	// Stop footer timer if running
	if w.footerTimer != nil {
		w.footerTimer.Stop()
		w.footerTimer = nil
	}

	// Stop compositor if running
	w.stopRenderLoop()
	closeEmulatorReplyPipe(w.emulator)
	w.emulator = nil

	var buf bytes.Buffer

	// Exit alternate screen if we're in one
	if w.altScreen {
		buf.WriteString("\x1b[?1049l")
		w.altScreen = false
	}

	// Disable any mouse modes moat mirrored onto the host so a crash or kill
	// mid-alt-screen doesn't leave the user's terminal stuck reporting mouse
	// events after moat exits.
	if dis := w.disableHostMouseModesLocked(); dis != nil {
		buf.Write(dis)
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
		fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[2K", height)
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
	fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[2K", w.height)
	buf.WriteString(w.bar.Render())

	// Restore cursor position
	buf.WriteString("\x1b[u")

	_, err := w.out.Write(buf.Bytes())
	return err
}

// SetMessage sets a temporary message overlay on the status bar.
// This replaces the normal status content until ClearMessage is called.
func (w *Writer) SetMessage(msg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.bar.SetMessage(msg)
}

// ClearMessage removes any message overlay and restores normal status display.
func (w *Writer) ClearMessage() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.bar.ClearMessage()
}

// SetJoinedCount updates the joined-agent count shown in the status footer.
// Calling this on SIGWINCH keeps the primary's "+N" badge live.
func (w *Writer) SetJoinedCount(n int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.bar.SetJoinedCount(n)
}

// RefreshFooter repaints the footer to reflect updated status-bar state.
// In compositor mode the render loop already repaints at renderInterval, so
// this only needs to act in scroll mode.
func (w *Writer) RefreshFooter() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cleanedUp {
		return
	}
	if !w.altScreen {
		w.redrawFooterLocked()
	}
}

// SetupEscapeHints configures the escape proxy to show escape sequence hints
// in the status bar when Ctrl-/ is pressed.
func (w *Writer) SetupEscapeHints(proxy *term.EscapeProxy) {
	proxy.OnPrefixChange(func(active bool) {
		if active {
			w.SetMessage("s (snapshot) · k (stop) · d (dump tui) · r (reset tui) · ctrl+/ (cancel)")
		} else {
			w.ClearMessage()
		}
		if err := w.UpdateStatus(); err != nil {
			log.Debug("failed to update status bar during escape hint toggle", "error", err)
		}
	})
}

// scheduleFooterRedrawLocked schedules a footer redraw after a debounce delay.
// If a timer is already running, it's reset. This ensures we only redraw after
// the child process has been quiet for footerDebounceDelay milliseconds.
// Caller must hold the mutex.
func (w *Writer) scheduleFooterRedrawLocked() {
	// Cancel existing timer if any
	if w.footerTimer != nil {
		w.footerTimer.Stop()
	}

	// Schedule new timer
	w.footerTimer = time.AfterFunc(w.footerDebounceDelay, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.redrawFooterLocked()
	})
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
