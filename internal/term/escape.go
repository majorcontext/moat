// Package term provides terminal utilities for interactive sessions.
package term

import (
	"errors"
	"io"
	"strconv"
	"strings"
)

// EscapeAction represents an action triggered by an escape sequence.
type EscapeAction int

const (
	// EscapeNone means no escape action was triggered.
	EscapeNone EscapeAction = iota
	// EscapeStop means the user wants to stop the run.
	EscapeStop
	// EscapeSnapshot means the user wants to take a manual snapshot.
	EscapeSnapshot
	// EscapeDumpTUI means the user wants to dump the TUI state for debugging.
	EscapeDumpTUI
	// EscapeResetTUI means the user wants to reset the TUI.
	EscapeResetTUI
)

// EscapeError is returned when an escape sequence is detected.
type EscapeError struct {
	Action EscapeAction
}

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

// IsEscapeError returns true if the error is an EscapeError.
func IsEscapeError(err error) bool {
	var escErr EscapeError
	return errors.As(err, &escErr)
}

// GetEscapeAction extracts the action from an EscapeError, or returns EscapeNone.
func GetEscapeAction(err error) EscapeAction {
	var escErr EscapeError
	if errors.As(err, &escErr) {
		return escErr.Action
	}
	return EscapeNone
}

const (
	// EscapePrefix is Ctrl-/ (0x1f)
	EscapePrefix byte = 0x1f

	// Command keys (after the prefix)
	escapeKeyStop     byte = 'k'
	escapeKeySnapshot byte = 's'
	escapeKeyDumpTUI  byte = 'd'
	escapeKeyResetTUI byte = 'r'
)

// Ctrl+/ under the kitty keyboard protocol.
//
// The legacy encoding of Ctrl+/ is the single byte 0x1f, which is
// indistinguishable from Ctrl+_ . An agent that enables the kitty keyboard
// protocol — Codex pushes "CSI > 7 u" at startup, Claude Code does not — asks
// the terminal to disambiguate those chords, and the terminal then reports
// Ctrl+/ as "CSI 47 ; 5 u" instead. Recognizing only the legacy byte means the
// escape prefix silently stops working under such an agent, including the
// ctrl+/ d dump that exists to debug it.
//
//	47 = '/' as a Unicode code point
//	 5 = Ctrl (bitmask 4) encoded as bitmask+1
const (
	kittyPrefixKeyCode = 47
	kittyPrefixMods    = 5

	// Kitty event types, reported when the "report event types" flag is on
	// (bit 2, which Codex's flag 7 includes). Absent means press.
	kittyEventPress   = 1
	kittyEventRelease = 3

	// maxKittySeqLen bounds how many bytes may be held while waiting for a
	// sequence to complete, so malformed input cannot stall the input stream.
	maxKittySeqLen = 24
)

// kittyMatch is the outcome of matchKittyPrefix.
type kittyMatch int

const (
	// kittyNone means data does not begin with the Ctrl+/ escape sequence.
	kittyNone kittyMatch = iota
	// kittyActivate means a press or repeat: treat it as the escape prefix.
	kittyActivate
	// kittyRelease means the key-up event for Ctrl+/. It must be swallowed
	// rather than acted on, otherwise the release that inevitably follows a
	// press would be read as the command key and eat the real one.
	kittyRelease
	// kittyPartial means data is a viable prefix and more bytes are needed.
	kittyPartial
)

// matchKittyPrefix reports whether data begins with the kitty encoding of
// Ctrl+/, returning the number of bytes the sequence occupies.
//
// Matching is deliberately narrow: only "CSI 47 ; 5 [:event] u" qualifies, so
// every other kitty-encoded key (and every other CSI sequence) flows through to
// the child untouched. Buffering for kittyPartial is likewise limited to input
// that could still become this one sequence, so unrelated escape sequences are
// never delayed.
// awaitingCommand widens matching while the escape prefix is armed: any key
// release is swallowed, not just Ctrl+/'s own. Releasing the chord can report
// the '/' key with different modifiers (lifting Ctrl first yields
// "CSI 47;1:3u") or report the modifier key itself, and any of those reaching
// the command-key branch would be read as an unrecognized command and cancel
// the prefix — the prefix would appear to "not stick".
func matchKittyPrefix(data []byte, awaitingCommand bool) (kittyMatch, int) {
	if len(data) == 0 || data[0] != 0x1b {
		return kittyNone, 0
	}
	if len(data) == 1 {
		return kittyPartial, 0
	}
	if data[1] != '[' {
		return kittyNone, 0
	}
	if len(data) > maxKittySeqLen {
		data = data[:maxKittySeqLen]
	}

	// Scan the parameter bytes up to the final byte.
	i := 2
	for i < len(data) && ((data[i] >= '0' && data[i] <= '9') || data[i] == ';' || data[i] == ':') {
		// Bail out as soon as the parameters cannot become "47;5". While the
		// prefix is armed any key's release qualifies, so every parameter
		// string stays viable.
		if !awaitingCommand && !viableKittyParams(string(data[2 : i+1])) {
			return kittyNone, 0
		}
		i++
	}
	if i >= len(data) {
		if len(data) >= maxKittySeqLen {
			return kittyNone, 0
		}
		return kittyPartial, 0
	}
	if data[i] != 'u' {
		return kittyNone, 0
	}

	key, mods, event, ok := parseKittyParams(string(data[2:i]))
	if !ok {
		return kittyNone, 0
	}
	// Any key-up while the prefix is armed: swallow it so it cannot be
	// mistaken for the command key.
	if awaitingCommand && event == kittyEventRelease {
		return kittyRelease, i + 1
	}
	if key != kittyPrefixKeyCode || mods != kittyPrefixMods {
		return kittyNone, 0
	}
	if event == kittyEventRelease {
		return kittyRelease, i + 1
	}
	return kittyActivate, i + 1
}

// viableKittyParams reports whether a partial parameter string could still
// grow into the Ctrl+/ parameters ("47;5" with an optional event sub-param).
func viableKittyParams(params string) bool {
	fields := strings.Split(params, ";")
	if len(fields) > 2 {
		return false
	}
	// First field: the key code, optionally with alternate-key sub-params.
	key := fields[0]
	if idx := strings.IndexByte(key, ':'); idx >= 0 {
		key = key[:idx]
	}
	want := "47"
	if len(key) > len(want) {
		return false
	}
	if key != want[:len(key)] {
		return false
	}
	if len(fields) == 1 {
		return true
	}
	// Second field: modifiers, optionally with an event sub-param.
	mods := fields[1]
	if idx := strings.IndexByte(mods, ':'); idx >= 0 {
		// Event sub-param may be anything; modifiers must already be complete.
		return mods[:idx] == "5"
	}
	return len(mods) == 0 || mods == "5"
}

// parseKittyParams extracts the key code, modifier field, and event type from
// a kitty CSI-u parameter string such as "47;5:3". Sub-parameters beyond the
// ones consulted (alternate key codes, text-as-codepoints) are ignored.
func parseKittyParams(params string) (key, mods, event int, ok bool) {
	fields := strings.Split(params, ";")
	if len(fields) == 0 || len(fields) > 2 {
		return 0, 0, 0, false
	}

	key, ok = atoiField(fields[0])
	if !ok {
		return 0, 0, 0, false
	}

	// A bare "CSI 47 u" carries no modifiers, so it is not Ctrl+/.
	if len(fields) == 1 {
		return key, 0, kittyEventPress, true
	}

	modField := fields[1]
	event = kittyEventPress
	if idx := strings.IndexByte(modField, ':'); idx >= 0 {
		var evOK bool
		event, evOK = atoiField(modField[idx+1:])
		if !evOK {
			return 0, 0, 0, false
		}
		modField = modField[:idx]
	}
	mods, ok = atoiField(modField)
	if !ok {
		return 0, 0, 0, false
	}
	return key, mods, event, true
}

// atoiField parses a single numeric parameter, taking only the portion before
// any sub-parameter separator.
func atoiField(s string) (int, bool) {
	if idx := strings.IndexByte(s, ':'); idx >= 0 {
		s = s[:idx]
	}
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// EscapeProxy wraps a reader and watches for escape sequences.
//
// Escape sequences are: Ctrl-/ followed by:
//   - s: take a snapshot (invokes onAction callback, continues reading)
//   - k: stop the run (returns EscapeError to unwind Read)
//   - d: dump TTY history (invokes onAction callback, continues reading)
//   - r: reset terminal (invokes onAction callback, continues reading)
//
// If Ctrl-/ is followed by an unrecognized key, both bytes are passed through.
// If Ctrl-/ is followed by another Ctrl-/, a single Ctrl-/ is passed through
// (allowing the user to send a literal Ctrl-/).
type EscapeProxy struct {
	r   io.Reader
	buf []byte // buffered bytes to return on next Read

	sawPrefix     bool         // true if we've seen Ctrl-/ and are waiting for next byte
	pendingEscape *EscapeError // escape detected but output pending first

	// partialSeq holds the start of a kitty Ctrl+/ sequence split across
	// reads, so it can be completed by the bytes of the next one.
	partialSeq []byte

	// onPrefixChange is called when the escape prefix state changes.
	// The callback receives true when Ctrl-/ is pressed (waiting for next key),
	// and false when the sequence completes or is canceled.
	onPrefixChange func(active bool)

	// onAction is called for non-disruptive escape actions (like snapshot)
	// that should not unwind Read() with an error. The callback receives the
	// action, and Read() continues normally after invoking it.
	onAction func(EscapeAction)
}

// NewEscapeProxy creates an EscapeProxy that wraps the given reader.
func NewEscapeProxy(r io.Reader) *EscapeProxy {
	return &EscapeProxy{r: r}
}

// OnPrefixChange sets a callback that fires when the escape prefix state changes.
// The callback receives true when Ctrl-/ is pressed (waiting for command key), and false
// when the sequence completes or is canceled. This can be used to update UI state,
// such as showing escape key hints in a status bar.
//
// The callback is invoked synchronously during Read() calls. Callers must ensure
// the callback doesn't block or cause deadlocks. If Read() can be called concurrently,
// the callback must be thread-safe.
func (e *EscapeProxy) OnPrefixChange(fn func(active bool)) {
	e.onPrefixChange = fn
}

// OnAction sets a callback for non-disruptive escape actions. Unlike EscapeStop
// which returns an EscapeError to unwind Read(), actions handled via OnAction
// (such as EscapeSnapshot) invoke the callback and continue reading.
//
// The callback is invoked synchronously during Read() calls. It should be
// non-blocking; long-running work should be dispatched to a goroutine.
func (e *EscapeProxy) OnAction(fn func(EscapeAction)) {
	e.onAction = fn
}

// setPrefixState updates the sawPrefix flag and notifies the callback if it changed.
func (e *EscapeProxy) setPrefixState(active bool) {
	if e.sawPrefix == active {
		return // no change
	}
	e.sawPrefix = active
	if e.onPrefixChange != nil {
		e.onPrefixChange(active)
	}
}

// Read implements io.Reader. It returns data from the underlying reader,
// filtering out escape sequences and returning EscapeError when detected.
func (e *EscapeProxy) Read(p []byte) (int, error) {
	// Check for pending escape from previous read
	if e.pendingEscape != nil {
		err := *e.pendingEscape
		e.pendingEscape = nil
		return 0, err
	}

	// Return any buffered data from a previous partial read
	if len(e.buf) > 0 {
		n := copy(p, e.buf)
		e.buf = e.buf[n:]
		return n, nil
	}

	// Read from underlying reader
	buf := make([]byte, len(p))
	n, err := e.r.Read(buf)
	if n == 0 {
		// If we had a pending prefix and hit EOF, return the prefix as literal
		if e.sawPrefix && err != nil {
			e.setPrefixState(false)
			p[0] = EscapePrefix
			return 1, err
		}
		// A held partial sequence can no longer complete; release its bytes to
		// the child rather than swallowing them.
		if len(e.partialSeq) > 0 && err != nil {
			held := e.partialSeq
			e.partialSeq = nil
			copied := copy(p, held)
			if copied < len(held) {
				e.buf = append(e.buf, held[copied:]...)
			}
			return copied, nil
		}
		return 0, err
	}

	// Prepend any partial kitty sequence held from the previous read.
	data := buf[:n]
	if len(e.partialSeq) > 0 {
		data = append(e.partialSeq, data...)
		e.partialSeq = nil
	}
	n = len(data)

	// Process the bytes, looking for escape sequences
	out := make([]byte, 0, n)
	var pendingEscape *EscapeError

scan:
	for i := 0; i < n; i++ {
		// Check for the kitty encoding of Ctrl+/ before anything else, so a
		// release event is swallowed rather than mistaken for a command key.
		if data[i] == 0x1b {
			switch m, length := matchKittyPrefix(data[i:], e.sawPrefix); m {
			case kittyNone:
				// Not our sequence; fall through to normal byte handling.

			case kittyPartial:
				// Hold the tail until the rest of the sequence arrives.
				e.partialSeq = append(e.partialSeq, data[i:]...)
				break scan

			case kittyRelease:
				// Key-up for Ctrl+/: consume without changing state.
				i += length - 1
				continue

			case kittyActivate:
				i += length - 1
				if e.sawPrefix {
					// Ctrl+/ Ctrl+/ sends a literal Ctrl+/, matching the
					// legacy double-prefix behavior.
					e.setPrefixState(false)
					out = append(out, EscapePrefix)
				} else {
					e.setPrefixState(true)
				}
				continue
			}
		}

		b := data[i]

		if e.sawPrefix {
			e.setPrefixState(false)

			// Check for escape commands
			switch b {
			case escapeKeyStop:
				if i+1 < n {
					e.buf = append(e.buf, data[i+1:n]...)
				}
				if len(out) > 0 {
					pendingEscape = &EscapeError{Action: EscapeStop}
					break
				}
				return 0, EscapeError{Action: EscapeStop}

			case escapeKeySnapshot:
				// Non-disruptive action: call callback and continue reading
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

			case EscapePrefix:
				// Ctrl-/ Ctrl-/ sends a single Ctrl-/
				out = append(out, EscapePrefix)

			default:
				// Not a recognized escape - pass through both bytes
				out = append(out, EscapePrefix, b)
			}
			continue
		}

		if b == EscapePrefix {
			// Start of potential escape sequence
			e.setPrefixState(true)
			continue
		}

		// Normal byte - pass through
		out = append(out, b)
	}

	// Handle pending escape after returning buffered output
	if pendingEscape != nil {
		// Store for next Read to return
		e.pendingEscape = pendingEscape

		// Copy output to caller's buffer
		copied := copy(p, out)
		if copied < len(out) {
			// Buffer the rest
			e.buf = append(e.buf, out[copied:]...)
		}
		return copied, nil
	}

	// If we ended with sawPrefix=true and have output, we need to
	// return the output first, then handle the prefix on next read.
	// The prefix is implicitly stored in sawPrefix.
	if e.sawPrefix && len(out) > 0 {
		// Return the output we have, handle the dangling prefix next time
		copied := copy(p, out)
		if copied < len(out) {
			e.buf = append(e.buf, out[copied:]...)
		}
		return copied, nil
	}

	// If we ended with sawPrefix=true and no output, we need to read more
	if e.sawPrefix && len(out) == 0 && err == nil {
		// We consumed all input and ended on a prefix - need to read one more byte
		// to determine the action.
		oneByte := make([]byte, 1)
		n2, err2 := e.r.Read(oneByte)
		if n2 == 0 {
			// EOF or error after prefix - treat prefix as literal
			e.setPrefixState(false)
			p[0] = EscapePrefix
			return 1, err2
		}

		// Process this byte as if it followed the prefix
		b := oneByte[0]
		e.setPrefixState(false)
		switch b {
		case escapeKeyStop:
			return 0, EscapeError{Action: EscapeStop}
		case escapeKeySnapshot:
			// Non-disruptive action: call callback and continue
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
		case EscapePrefix:
			// Send single prefix
			p[0] = EscapePrefix
			return 1, nil
		default:
			// Not an escape - send both bytes
			p[0] = EscapePrefix
			if len(p) > 1 {
				p[1] = b
				return 2, nil
			}
			// Buffer the second byte
			e.buf = append(e.buf, b)
			return 1, nil
		}
	}

	// If sawPrefix is true but we have EOF, treat the prefix as literal
	if e.sawPrefix && err != nil {
		e.setPrefixState(false)
		out = append(out, EscapePrefix)
	}

	// A held partial sequence that will never complete is not an escape prefix
	// after all — release the bytes to the child rather than swallowing them.
	if len(e.partialSeq) > 0 && err != nil {
		out = append(out, e.partialSeq...)
		e.partialSeq = nil
	}

	// Holding a partial with nothing else to return would mean (0, nil), which
	// callers are entitled to treat as "nothing happened". Read again so the
	// sequence can complete instead.
	if len(out) == 0 && len(e.partialSeq) > 0 && err == nil {
		return e.Read(p)
	}

	// Copy output to caller's buffer
	copied := copy(p, out)
	if copied < len(out) {
		// Buffer the rest
		e.buf = append(e.buf, out[copied:]...)
	}

	if copied == 0 && err != nil {
		return 0, err
	}
	return copied, nil
}

// EscapeHelpText returns help text explaining the escape sequences.
func EscapeHelpText() string {
	return "ctrl+/ s (snapshot) · k (stop) · d (dump tui) · r (reset tui)"
}
