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

// Content returns the status bar content string (with ANSI styling).
func (s *StatusBar) Content() string {
	// Build left and right sections with reverse video (inverted colors):
	//  moat                                      run_id · name
	left := " moat "
	right := fmt.Sprintf(" %s · %s ", s.runID, s.name)

	leftLen := runeLen(left)
	rightLen := runeLen(right)
	totalLen := leftLen + rightLen

	if totalLen >= s.width {
		// Need to truncate
		return s.truncatedContent()
	}

	// Fill middle with spaces (will be styled with reverse video)
	middleLen := s.width - totalLen
	content := left + strings.Repeat(" ", middleLen) + right

	// Wrap with reverse video escape codes
	return "\x1b[7m" + content + "\x1b[0m"
}

// truncatedContent returns a shortened version for narrow terminals.
func (s *StatusBar) truncatedContent() string {
	left := " moat "

	// Try with name
	rightWithName := fmt.Sprintf(" %s · %s ", s.runID, s.name)
	if runeLen(left)+runeLen(rightWithName) <= s.width {
		middleLen := s.width - runeLen(left) - runeLen(rightWithName)
		content := left + strings.Repeat(" ", middleLen) + rightWithName
		return "\x1b[7m" + content + "\x1b[0m"
	}

	// Try without name - just run ID
	rightMinimal := fmt.Sprintf(" %s ", s.runID)
	if runeLen(left)+runeLen(rightMinimal) <= s.width {
		middleLen := s.width - runeLen(left) - runeLen(rightMinimal)
		content := left + strings.Repeat(" ", middleLen) + rightMinimal
		return "\x1b[7m" + content + "\x1b[0m"
	}

	// Extremely narrow - just fill with spaces
	return "\x1b[7m" + strings.Repeat(" ", s.width) + "\x1b[0m"
}

// runeLen returns the display width of a string (counting runes).
func runeLen(s string) int {
	return len([]rune(s))
}
