// Package term provides terminal utilities for interactive sessions.
package term

import (
	"errors"
	"io"
)

// EscapeAction represents an action triggered by an escape sequence.
type EscapeAction int

const (
	// EscapeNone means no escape action was triggered.
	EscapeNone EscapeAction = iota
	// EscapeDetach means the user wants to detach from the session.
	EscapeDetach
	// EscapeStop means the user wants to stop the run.
	EscapeStop
)

// EscapeError is returned when an escape sequence is detected.
type EscapeError struct {
	Action EscapeAction
}

func (e EscapeError) Error() string {
	switch e.Action {
	case EscapeDetach:
		return "escape: detach"
	case EscapeStop:
		return "escape: stop"
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
	escapeKeyDetach byte = 'd'
	escapeKeyStop   byte = 'k'
)

// EscapeProxy wraps a reader and watches for escape sequences.
//
// Escape sequences are: Ctrl-/ followed by:
//   - d: detach from session (run continues)
//   - k: stop the run
//
// When an escape sequence is detected, Read returns an EscapeError.
// If Ctrl-/ is followed by an unrecognized key, both bytes are passed through.
// If Ctrl-/ is followed by another Ctrl-/, a single Ctrl-/ is passed through
// (allowing the user to send a literal Ctrl-/).
type EscapeProxy struct {
	r   io.Reader
	buf []byte // buffered bytes to return on next Read

	sawPrefix     bool         // true if we've seen Ctrl-/ and are waiting for next byte
	pendingEscape *EscapeError // escape detected but output pending first
}

// NewEscapeProxy creates an EscapeProxy that wraps the given reader.
func NewEscapeProxy(r io.Reader) *EscapeProxy {
	return &EscapeProxy{r: r}
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
			e.sawPrefix = false
			p[0] = EscapePrefix
			return 1, err
		}
		return 0, err
	}

	// Process the bytes, looking for escape sequences
	out := make([]byte, 0, n)
	var pendingEscape *EscapeError

	for i := 0; i < n; i++ {
		b := buf[i]

		if e.sawPrefix {
			e.sawPrefix = false

			// Check for escape commands
			switch b {
			case escapeKeyDetach:
				// Buffer any remaining bytes for next Read
				if i+1 < n {
					e.buf = append(e.buf, buf[i+1:n]...)
				}
				// If we have output to return first, defer the escape
				if len(out) > 0 {
					pendingEscape = &EscapeError{Action: EscapeDetach}
				} else {
					return 0, EscapeError{Action: EscapeDetach}
				}

			case escapeKeyStop:
				if i+1 < n {
					e.buf = append(e.buf, buf[i+1:n]...)
				}
				if len(out) > 0 {
					pendingEscape = &EscapeError{Action: EscapeStop}
				} else {
					return 0, EscapeError{Action: EscapeStop}
				}

			case EscapePrefix:
				// Ctrl-/ Ctrl-/ sends a single Ctrl-/
				out = append(out, EscapePrefix)
				continue

			default:
				// Not a recognized escape - pass through both bytes
				out = append(out, EscapePrefix, b)
				continue
			}
			// If we set pendingEscape, break out of the loop
			if pendingEscape != nil {
				break
			}
			continue
		}

		if b == EscapePrefix {
			// Start of potential escape sequence
			e.sawPrefix = true
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
			e.sawPrefix = false
			p[0] = EscapePrefix
			return 1, err2
		}

		// Process this byte as if it followed the prefix
		b := oneByte[0]
		e.sawPrefix = false
		switch b {
		case escapeKeyDetach:
			return 0, EscapeError{Action: EscapeDetach}
		case escapeKeyStop:
			return 0, EscapeError{Action: EscapeStop}
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
		e.sawPrefix = false
		out = append(out, EscapePrefix)
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
	return "Escape sequences: Ctrl-/ d (detach), Ctrl-/ k (stop)"
}
