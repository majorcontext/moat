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

// Render returns the full status bar with ANSI escapes for positioning.
// The caller is responsible for cursor positioning after calling Render.
func (s *StatusBar) Render() string {
	if s.height <= 0 {
		return ""
	}
	// Move to bottom row, clear line, draw bar.
	// Note: No save/restore cursor here - Writer.renderLocked() handles cursor
	// positioning explicitly to avoid double cursor artifacts.
	return fmt.Sprintf("\x1b[%d;1H\x1b[2K%s", s.height, s.Content())
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
	return s.buildContent(true, true)
}

// buildContent constructs the status bar with optional grants and name.
func (s *StatusBar) buildContent(showGrants, showName bool) string {
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

	// Build right segment
	var rightPlain string
	if showName && s.name != "" {
		rightPlain = fmt.Sprintf(" %s · %s ", s.runID, s.name)
	} else {
		rightPlain = fmt.Sprintf(" %s ", s.runID)
	}

	totalPlain := runeLen(leftPlain) + runeLen(runtimePlain) + runeLen(grantsPlain) + runeLen(rightPlain)

	if totalPlain > s.width {
		// Truncation cascade
		if showGrants && len(s.grants) > 0 {
			return s.buildContent(false, showName)
		}
		if showName && s.name != "" {
			return s.buildContent(false, false)
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
	buf.WriteString(rightPlain)
	buf.WriteString(reset)

	return buf.String()
}

// runeLen returns the display width of a string (counting runes).
func runeLen(s string) int {
	return len([]rune(s))
}
