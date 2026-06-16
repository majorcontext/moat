// internal/tui/statusbar.go
package tui

import (
	"fmt"
	"strings"
)

// StatusBar renders run context at the bottom of the terminal.
type StatusBar struct {
	runID       string
	name        string
	runtime     string
	grants      []string
	warning     string // persistent warning shown between grants and right side
	session     string // explicit session role for a joined session, e.g. "joined · 2"
	joinedCount int    // number of joined agents (shown as "+N" on the primary)
	width       int
	height      int
	message     string // optional message overlay that replaces normal content
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

// SetGrants sets the granted credentials to display.
func (s *StatusBar) SetGrants(grants []string) {
	s.grants = grants
}

// SetWarning sets a persistent warning shown in the status bar (e.g. "proxy stale").
func (s *StatusBar) SetWarning(warning string) {
	s.warning = warning
}

// SetSession sets an explicit session-role label shown before the run-id
// (e.g. "joined · 2"). Empty means a primary session.
func (s *StatusBar) SetSession(label string) {
	s.session = label
}

// SetJoinedCount sets the number of joined agents, rendered as "+N" before the
// run-id on a primary session. Zero renders nothing.
func (s *StatusBar) SetJoinedCount(n int) {
	s.joinedCount = n
}

// sessionBase returns the session segment text without any trailing separator,
// or "" when there is nothing to show.
//
// Rules:
//   - s.session != "" → a joined session; returns s.session (e.g. "joined · 2"), green.
//   - s.joinedCount > 0 → the primary with joins; returns "primary +N", red.
//   - otherwise → solo primary; returns "" (no segment).
func (s *StatusBar) sessionBase() string {
	if s.session != "" {
		return s.session
	}
	if s.joinedCount > 0 {
		return fmt.Sprintf("primary +%d", s.joinedCount)
	}
	return ""
}

// sessionPlain returns the plain-text session segment followed by " · " when
// non-empty, or "" when there is nothing to show. Used for width measurement.
func (s *StatusBar) sessionPlain() string {
	if b := s.sessionBase(); b != "" {
		return b + " · "
	}
	return ""
}

// SetDimensions sets terminal width and height.
func (s *StatusBar) SetDimensions(width, height int) {
	s.width = width
	s.height = height
}

// SetMessage sets a temporary message overlay that replaces the normal status bar content.
// This is useful for displaying context-sensitive information like escape sequence hints.
// Call ClearMessage() to restore normal status display.
func (s *StatusBar) SetMessage(msg string) {
	s.message = msg
}

// ClearMessage removes any message overlay and restores normal status bar content.
func (s *StatusBar) ClearMessage() {
	s.message = ""
}

// Render returns the status bar content with ANSI styling.
// The caller is responsible for cursor positioning and line clearing.
func (s *StatusBar) Render() string {
	return s.Content()
}

// ANSI escape sequences for status bar styling.
const (
	bgDarkGray = "\x1b[48;5;236m"
	fgCyan     = "\x1b[36m"
	fgGreen    = "\x1b[32m"
	fgMagenta  = "\x1b[35m"
	fgRed      = "\x1b[31m"
	fgYellow   = "\x1b[33m"
	bold       = "\x1b[1m"
	dim        = "\x1b[2m"
	reset      = "\x1b[0m"
)

// runtimeColor returns the ANSI color code for the given runtime type.
func runtimeColor(runtime string) string {
	switch runtime {
	case "docker":
		return fgCyan
	case "apple":
		return fgMagenta
	default:
		return ""
	}
}

// Content returns the status bar content string (with ANSI styling).
func (s *StatusBar) Content() string {
	// If a message overlay is set, display it instead of normal content
	if s.message != "" {
		return s.renderMessage()
	}
	return s.buildContent(true, true, true, true, true)
}

// buildContent constructs the status bar with optional grants, name, hint, and session.
func (s *StatusBar) buildContent(showGrants, showName, showHint, showWarning, showSession bool) string {
	// Build left segments (plain text for measurement)
	leftPlain := " moat "
	runtimePlain := ""
	if s.runtime != "" {
		runtimePlain = " " + s.runtime + " "
	}
	grantsPlain := ""
	if showGrants && len(s.grants) > 0 {
		grantsPlain = " " + strings.Join(s.grants, " · ") + " "
	}
	warningPlain := ""
	if showWarning && s.warning != "" {
		warningPlain = " " + s.warning + " "
	}

	// Build right segment with optional session segment and hint
	var rightPlain string
	hintPlain := ""
	if showHint {
		hintPlain = " (ctrl+/)"
	}

	sessionSeg := ""
	if showSession {
		sessionSeg = s.sessionPlain()
	}

	if showName && s.name != "" {
		rightPlain = fmt.Sprintf(" %s%s · %s%s ", sessionSeg, s.runID, s.name, hintPlain)
	} else {
		rightPlain = fmt.Sprintf(" %s%s%s ", sessionSeg, s.runID, hintPlain)
	}

	totalPlain := runeLen(leftPlain) + runeLen(runtimePlain) + runeLen(grantsPlain) + runeLen(warningPlain) + runeLen(rightPlain)

	if totalPlain > s.width {
		// Truncation cascade: drop hint first, then grants, then name, then session.
		// Warning is kept as long as possible (dropped last) since it's diagnostic.
		if showHint {
			return s.buildContent(showGrants, showName, false, showWarning, showSession)
		}
		if showGrants && len(s.grants) > 0 {
			return s.buildContent(false, showName, false, showWarning, showSession)
		}
		if showName && s.name != "" {
			return s.buildContent(false, false, false, showWarning, showSession)
		}
		if showSession && s.sessionPlain() != "" {
			return s.buildContent(false, false, false, showWarning, false)
		}
		if showWarning && s.warning != "" {
			return s.buildContent(false, false, false, false, false)
		}
		if runeLen(leftPlain)+runeLen(rightPlain) > s.width {
			// Just spaces
			return bgDarkGray + strings.Repeat(" ", s.width) + reset
		}
	}

	// Build styled content
	middleLen := s.width - totalPlain
	if middleLen < 0 {
		middleLen = 0
	}

	var buf strings.Builder
	buf.WriteString(bgDarkGray)
	buf.WriteString(bold)
	buf.WriteString(leftPlain)
	buf.WriteString(reset)
	buf.WriteString(bgDarkGray)

	if runtimePlain != "" {
		color := runtimeColor(s.runtime)
		if color != "" {
			buf.WriteString(color)
		}
		buf.WriteString(runtimePlain)
		if color != "" {
			buf.WriteString(reset)
			buf.WriteString(bgDarkGray)
		}
	}

	if grantsPlain != "" {
		buf.WriteString(dim)
		buf.WriteString(grantsPlain)
		buf.WriteString(reset)
		buf.WriteString(bgDarkGray)
	}

	if warningPlain != "" {
		buf.WriteString(fgYellow)
		buf.WriteString(warningPlain)
		buf.WriteString(reset)
		buf.WriteString(bgDarkGray)
	}

	buf.WriteString(strings.Repeat(" ", middleLen))

	// Build right segment with styling
	buf.WriteString(" ")
	if showSession {
		if base := s.sessionBase(); base != "" {
			// Color: green for joined sessions, red for primary-with-joins.
			color := fgGreen
			if s.session == "" {
				color = fgRed
			}
			buf.WriteString(color)
			buf.WriteString(base)
			buf.WriteString(reset)
			buf.WriteString(bgDarkGray)
			// Separator rendered in default footer style (not colored).
			buf.WriteString(" · ")
		}
	}
	if showName && s.name != "" {
		buf.WriteString(fmt.Sprintf("%s · %s", s.runID, s.name))
	} else {
		buf.WriteString(s.runID)
	}

	// Add hint in dim style
	if showHint {
		buf.WriteString(" ")
		buf.WriteString(dim)
		buf.WriteString("(ctrl+/)")
		buf.WriteString(reset)
		buf.WriteString(bgDarkGray)
	}

	buf.WriteString(" ")
	buf.WriteString(reset)

	return buf.String()
}

// renderMessage renders a message overlay with styling.
func (s *StatusBar) renderMessage() string {
	msgPlain := " " + s.message + " "
	msgLen := runeLen(msgPlain)

	// If message is too long, truncate it
	if msgLen > s.width {
		// Keep as much as fits
		runes := []rune(msgPlain)
		if s.width > 3 {
			msgPlain = string(runes[:s.width-3]) + "..."
		} else if s.width > 0 {
			msgPlain = string(runes[:s.width])
		} else {
			msgPlain = ""
		}
		msgLen = s.width
	}

	// Calculate padding
	padding := s.width - msgLen
	leftPad := padding / 2
	rightPad := padding - leftPad

	var buf strings.Builder
	buf.WriteString(bgDarkGray)
	buf.WriteString(strings.Repeat(" ", leftPad))
	buf.WriteString(bold)
	buf.WriteString(msgPlain)
	buf.WriteString(reset)
	buf.WriteString(bgDarkGray)
	buf.WriteString(strings.Repeat(" ", rightPad))
	buf.WriteString(reset)

	return buf.String()
}

// runeLen returns the display width of a string (counting runes).
func runeLen(s string) int {
	return len([]rune(s))
}
