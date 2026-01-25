// internal/tui/statusbar_test.go
package tui

import (
	"regexp"
	"strings"
	"testing"
)

// stripANSI removes ANSI escape codes from a string for width measurement
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

func TestStatusBar_Render(t *testing.T) {
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(60, 24)

	rendered := bar.Content()

	// Should contain key components (runtime is not displayed)
	if !strings.Contains(rendered, "moat") {
		t.Errorf("expected 'moat' in bar, got %q", rendered)
	}
	if !strings.Contains(rendered, "run_abc123") {
		t.Errorf("expected run ID in bar, got %q", rendered)
	}
	if !strings.Contains(rendered, "my-agent") {
		t.Errorf("expected name in bar, got %q", rendered)
	}
}

func TestStatusBar_NarrowWidth(t *testing.T) {
	bar := NewStatusBar("run_abc123", "very-long-agent-name-that-should-be-truncated", "docker")
	bar.SetDimensions(40, 24)

	rendered := bar.Content()

	// Run ID should be preserved, name may be truncated
	if !strings.Contains(rendered, "run_abc123") {
		t.Errorf("expected run ID preserved in narrow bar, got %q", rendered)
	}
	// Strip ANSI codes and use rune count for width check
	stripped := stripANSI(rendered)
	runeCount := len([]rune(stripped))
	if runeCount > 40 {
		t.Errorf("expected bar width <= 40 runes, got %d", runeCount)
	}
}

func TestStatusBar_RenderEscaped(t *testing.T) {
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(60, 24)

	rendered := bar.Render()

	// Should contain cursor positioning
	if !strings.Contains(rendered, "\x1b[24;1H") { // move to row 24, col 1
		t.Errorf("expected cursor move escape, got %q", rendered)
	}
	// Should contain clear line
	if !strings.Contains(rendered, "\x1b[2K") {
		t.Errorf("expected clear line escape, got %q", rendered)
	}
	// Should contain save cursor
	if !strings.Contains(rendered, "\x1b[s") {
		t.Errorf("expected save cursor escape, got %q", rendered)
	}
	// Should contain restore cursor
	if !strings.Contains(rendered, "\x1b[u") {
		t.Errorf("expected restore cursor escape, got %q", rendered)
	}
	// Should contain the actual status bar content
	if !strings.Contains(rendered, "moat") {
		t.Errorf("expected status bar content, got %q", rendered)
	}
}

func TestStatusBar_RenderEscaped_ZeroHeight(t *testing.T) {
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(60, 0)

	rendered := bar.Render()

	// Should return empty string for height <= 0
	if rendered != "" {
		t.Errorf("expected empty string for height=0, got %q", rendered)
	}
}
