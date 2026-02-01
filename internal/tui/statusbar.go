// internal/tui/statusbar.go
package tui

import (
	"fmt"
	"strings"
)

// StatusBar renders run context at the bottom of the terminal.
type StatusBar struct {
	runID   string
	name    string
	runtime string
	grants  []string
	width   int
	height  int
	message string // optional message overlay that replaces normal content
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
	fgMagenta  = "\x1b[35m"
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
	return s.buildContent(true, true, true)
}

// buildContent constructs the status bar with optional grants, name, and hint.
func (s *StatusBar) buildContent(showGrants, showName, showHint bool) string {
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

	// Build right segment with optional hint
	var rightPlain string
	hintPlain := ""
	if showHint {
		hintPlain = " (ctrl+/)"
	}

	if showName && s.name != "" {
		rightPlain = fmt.Sprintf(" %s · %s%s ", s.runID, s.name, hintPlain)
	} else {
		rightPlain = fmt.Sprintf(" %s%s ", s.runID, hintPlain)
	}

	totalPlain := runeLen(leftPlain) + runeLen(runtimePlain) + runeLen(grantsPlain) + runeLen(rightPlain)

	if totalPlain > s.width {
		// Truncation cascade: drop hint, then grants, then name
		if showHint {
			return s.buildContent(showGrants, showName, false)
		}
		if showGrants && len(s.grants) > 0 {
			return s.buildContent(false, showName, false)
		}
		if showName && s.name != "" {
			return s.buildContent(false, false, false)
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

	buf.WriteString(strings.Repeat(" ", middleLen))

	// Build right segment with styling
	if showName && s.name != "" {
		buf.WriteString(fmt.Sprintf(" %s · %s", s.runID, s.name))
	} else {
		buf.WriteString(fmt.Sprintf(" %s", s.runID))
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
