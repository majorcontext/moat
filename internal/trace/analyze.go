package trace

import (
	"bytes"
	"fmt"
	"strings"
)

// DecodedEvent represents a trace event with decoded control sequences.
type DecodedEvent struct {
	Event
	Decoded string // human-readable representation
}

// Decode returns a human-readable representation of trace events.
func (t *Trace) Decode() []DecodedEvent {
	result := make([]DecodedEvent, 0, len(t.Events))
	for _, event := range t.Events {
		decoded := DecodeEvent(event)
		result = append(result, DecodedEvent{
			Event:   event,
			Decoded: decoded,
		})
	}
	return result
}

// DecodeEvent decodes a single event.
func DecodeEvent(e Event) string {
	switch e.Type {
	case EventResize:
		if e.Size != nil {
			return fmt.Sprintf("RESIZE %dx%d", e.Size.Width, e.Size.Height)
		}
		return "RESIZE (no size)"
	case EventSignal:
		return fmt.Sprintf("SIGNAL %s", e.Signal)
	case EventStdout, EventStderr, EventStdin:
		return decodeBytes(e.Data)
	default:
		return fmt.Sprintf("UNKNOWN(%s)", e.Type)
	}
}

// decodeBytes decodes control sequences and text in byte data.
func decodeBytes(data []byte) string {
	var parts []string
	i := 0

	for i < len(data) {
		// Check for escape sequences
		if data[i] == 0x1b && i+1 < len(data) {
			seq, length := parseEscapeSequence(data[i:])
			parts = append(parts, seq)
			i += length
		} else if data[i] == '\r' {
			parts = append(parts, "CR")
			i++
		} else if data[i] == '\n' {
			parts = append(parts, "LF")
			i++
		} else if data[i] == '\t' {
			parts = append(parts, "TAB")
			i++
		} else if data[i] < 32 || data[i] == 127 {
			// Other control characters
			parts = append(parts, fmt.Sprintf("^%c", data[i]+64))
			i++
		} else {
			// Regular text - collect consecutive printable chars
			start := i
			for i < len(data) && data[i] >= 32 && data[i] != 127 && data[i] != 0x1b {
				i++
			}
			text := string(data[start:i])
			// Truncate long text
			if len(text) > 40 {
				text = text[:37] + "..."
			}
			parts = append(parts, fmt.Sprintf("%q", text))
		}
	}

	return strings.Join(parts, " ")
}

// parseEscapeSequence identifies and formats an ANSI escape sequence.
func parseEscapeSequence(data []byte) (string, int) {
	if len(data) < 2 {
		return "ESC", 1
	}

	switch data[1] {
	case '[': // CSI sequence
		return parseCSI(data)
	case ']': // OSC sequence
		return parseOSC(data)
	case '(', ')': // Character set selection
		if len(data) >= 3 {
			return fmt.Sprintf("ESC%c%c (charset)", data[1], data[2]), 3
		}
		return fmt.Sprintf("ESC%c (incomplete)", data[1]), 2
	case '7': // DECSC - Save cursor
		return "ESC7 (save cursor)", 2
	case '8': // DECRC - Restore cursor
		return "ESC8 (restore cursor)", 2
	case 'M': // RI - Reverse index
		return "ESCM (reverse index)", 2
	case 'c': // RIS - Reset to initial state
		return "ESCc (reset)", 2
	default:
		return fmt.Sprintf("ESC%c", data[1]), 2
	}
}

// parseCSI parses CSI (Control Sequence Introducer) sequences: ESC[...
func parseCSI(data []byte) (string, int) {
	// Find the end of the sequence (first letter after ESC[)
	i := 2
	for i < len(data) && !isCSITerminator(data[i]) {
		i++
	}
	if i >= len(data) {
		return "ESC[ (incomplete CSI)", len(data)
	}

	params := string(data[2:i])
	cmd := data[i]
	length := i + 1

	// Decode common sequences
	switch cmd {
	case 'A':
		return fmt.Sprintf("ESC[%sA (cursor up %s)", params, paramOrDefault(params, "1")), length
	case 'B':
		return fmt.Sprintf("ESC[%sB (cursor down %s)", params, paramOrDefault(params, "1")), length
	case 'C':
		return fmt.Sprintf("ESC[%sC (cursor forward %s)", params, paramOrDefault(params, "1")), length
	case 'D':
		return fmt.Sprintf("ESC[%sD (cursor back %s)", params, paramOrDefault(params, "1")), length
	case 'H', 'f':
		return fmt.Sprintf("ESC[%sH (cursor position %s)", params, paramOrDefault(params, "1;1")), length
	case 'J':
		mode := paramOrDefault(params, "0")
		desc := "to end"
		if mode == "1" {
			desc = "to beginning"
		} else if mode == "2" {
			desc = "entire screen"
		} else if mode == "3" {
			desc = "entire screen+scrollback"
		}
		return fmt.Sprintf("ESC[%sJ (clear %s)", params, desc), length
	case 'K':
		mode := paramOrDefault(params, "0")
		desc := "to end of line"
		if mode == "1" {
			desc = "to beginning of line"
		} else if mode == "2" {
			desc = "entire line"
		}
		return fmt.Sprintf("ESC[%sK (clear %s)", params, desc), length
	case 'm':
		return fmt.Sprintf("ESC[%sm (SGR: %s)", params, decodeSGR(params)), length
	case 's':
		return "ESC[s (save cursor)", length
	case 'u':
		return "ESC[u (restore cursor)", length
	case 'r':
		return fmt.Sprintf("ESC[%sr (set scroll region %s)", params, paramOrDefault(params, "full")), length
	case 'h', 'l':
		mode := "set"
		if cmd == 'l' {
			mode = "reset"
		}
		return fmt.Sprintf("ESC[%s%c (%s mode %s)", params, cmd, mode, params), length
	default:
		return fmt.Sprintf("ESC[%s%c", params, cmd), length
	}
}

// parseOSC parses OSC (Operating System Command) sequences: ESC]...
func parseOSC(data []byte) (string, int) {
	// OSC sequences end with BEL (0x07) or ST (ESC\)
	for i := 2; i < len(data); i++ {
		if data[i] == 0x07 {
			return fmt.Sprintf("ESC]%s BEL (OSC)", string(data[2:i])), i + 1
		}
		if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '\\' {
			return fmt.Sprintf("ESC]%s ST (OSC)", string(data[2:i])), i + 2
		}
	}
	return "ESC] (incomplete OSC)", len(data)
}

// isCSITerminator returns true if the byte terminates a CSI sequence.
func isCSITerminator(b byte) bool {
	return b >= 0x40 && b <= 0x7E
}

// paramOrDefault returns params if non-empty, otherwise default value.
func paramOrDefault(params, def string) string {
	if params == "" {
		return def
	}
	return params
}

// decodeSGR decodes SGR (Select Graphic Rendition) parameters.
func decodeSGR(params string) string {
	if params == "" || params == "0" {
		return "reset"
	}
	// Common SGR codes
	parts := strings.Split(params, ";")
	var decoded []string
	for _, p := range parts {
		switch p {
		case "1":
			decoded = append(decoded, "bold")
		case "2":
			decoded = append(decoded, "dim")
		case "3":
			decoded = append(decoded, "italic")
		case "4":
			decoded = append(decoded, "underline")
		case "7":
			decoded = append(decoded, "reverse")
		case "22":
			decoded = append(decoded, "normal-intensity")
		case "23":
			decoded = append(decoded, "not-italic")
		case "24":
			decoded = append(decoded, "not-underline")
		case "27":
			decoded = append(decoded, "not-reverse")
		default:
			if strings.HasPrefix(p, "3") || strings.HasPrefix(p, "4") || strings.HasPrefix(p, "9") || strings.HasPrefix(p, "10") {
				decoded = append(decoded, "color:"+p)
			} else {
				decoded = append(decoded, p)
			}
		}
	}
	return strings.Join(decoded, ",")
}

// FindClearScreen returns events that clear the screen.
func (t *Trace) FindClearScreen() []int {
	var indices []int
	for i, event := range t.Events {
		if event.Type == EventStdout || event.Type == EventStderr {
			if containsClearScreen(event.Data) {
				indices = append(indices, i)
			}
		}
	}
	return indices
}

// containsClearScreen returns true if data contains a clear screen sequence.
func containsClearScreen(data []byte) bool {
	// ESC[2J or ESC[3J
	return bytes.Contains(data, []byte("\x1b[2J")) || bytes.Contains(data, []byte("\x1b[3J"))
}

// FindResizeIssues finds cases where screen is cleared shortly after resize.
// This can cause TUI rendering issues if the app clears before processing resize.
func (t *Trace) FindResizeIssues(windowNanos int64) []string {
	var issues []string
	for i, event := range t.Events {
		if event.Type == EventResize {
			// Look for clear screen within window after resize
			for j := i + 1; j < len(t.Events); j++ {
				if t.Events[j].TimestampNano-event.TimestampNano > windowNanos {
					break
				}
				if (t.Events[j].Type == EventStdout || t.Events[j].Type == EventStderr) &&
					containsClearScreen(t.Events[j].Data) {
					issues = append(issues, fmt.Sprintf(
						"Event %d: Clear screen %dμs after resize %dx%d at event %d",
						j,
						(t.Events[j].TimestampNano-event.TimestampNano)/1000,
						event.Size.Width,
						event.Size.Height,
						i,
					))
				}
			}
		}
	}
	return issues
}
