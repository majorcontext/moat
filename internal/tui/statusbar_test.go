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

	// Should contain key components
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

	// Render() should now return just the styled content without positioning
	// (positioning is handled by the caller in writer.go)

	// Should NOT contain cursor positioning (that's the caller's responsibility)
	if strings.Contains(rendered, "\x1b[24;1H") {
		t.Errorf("unexpected cursor move escape (caller handles positioning), got %q", rendered)
	}
	// Should NOT contain clear line (that's the caller's responsibility)
	if strings.Contains(rendered, "\x1b[2K") {
		t.Errorf("unexpected clear line escape (caller handles clearing), got %q", rendered)
	}
	// Should NOT contain save/restore cursor
	if strings.Contains(rendered, "\x1b[s") {
		t.Errorf("unexpected save cursor escape (caller handles cursor), got %q", rendered)
	}
	if strings.Contains(rendered, "\x1b[u") {
		t.Errorf("unexpected restore cursor escape (caller handles cursor), got %q", rendered)
	}
	// Should contain the actual status bar content with styling
	if !strings.Contains(rendered, "moat") {
		t.Errorf("expected status bar content, got %q", rendered)
	}
	// Should contain ANSI styling codes
	if !strings.Contains(rendered, "\x1b[") {
		t.Errorf("expected ANSI styling codes, got %q", rendered)
	}
}

func TestStatusBar_RenderEscaped_ZeroHeight(t *testing.T) {
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(60, 0)

	rendered := bar.Render()

	// Render() now just returns Content(), which doesn't check height
	// (height checking happens in the caller). So this should return content.
	if rendered == "" {
		t.Error("expected non-empty content even with height=0 (caller handles height checks)")
	}
	if !strings.Contains(rendered, "moat") {
		t.Errorf("expected status bar content, got %q", rendered)
	}
}

func TestStatusBar_RuntimeDisplay(t *testing.T) {
	tests := []struct {
		runtime   string
		wantColor string
	}{
		{"docker", "\x1b[36m"}, // cyan
		{"apple", "\x1b[35m"},  // magenta
		{"unknown", ""},
	}
	for _, tt := range tests {
		t.Run(tt.runtime, func(t *testing.T) {
			bar := NewStatusBar("run_abc", "agent", tt.runtime)
			bar.SetDimensions(80, 24)
			content := bar.Content()

			if !strings.Contains(content, tt.runtime) {
				t.Errorf("expected runtime %q in bar, got %q", tt.runtime, content)
			}
			if tt.wantColor != "" && !strings.Contains(content, tt.wantColor) {
				t.Errorf("expected color %q for runtime %q, got %q", tt.wantColor, tt.runtime, content)
			}
		})
	}
}

func TestStatusBar_GrantsDisplay(t *testing.T) {
	bar := NewStatusBar("run_abc", "agent", "docker")
	bar.SetGrants([]string{"github", "ssh"})
	bar.SetDimensions(80, 24)

	content := bar.Content()

	if !strings.Contains(content, "github") {
		t.Errorf("expected 'github' grant in bar, got %q", content)
	}
	if !strings.Contains(content, "ssh") {
		t.Errorf("expected 'ssh' grant in bar, got %q", content)
	}
	// Grants should be dim
	if !strings.Contains(content, "\x1b[2m") {
		t.Errorf("expected dim escape for grants, got %q", content)
	}
}

func TestStatusBar_DarkGrayBackground(t *testing.T) {
	bar := NewStatusBar("run_abc", "agent", "docker")
	bar.SetDimensions(80, 24)

	content := bar.Content()

	// Should use dark gray background instead of reverse video
	if !strings.Contains(content, "\x1b[48;5;236m") {
		t.Errorf("expected dark gray background, got %q", content)
	}
	if strings.Contains(content, "\x1b[7m") {
		t.Errorf("unexpected reverse video escape, got %q", content)
	}
}

func TestStatusBar_TruncationCascade(t *testing.T) {
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetGrants([]string{"github", "ssh", "aws"})

	// Wide enough for everything
	bar.SetDimensions(80, 24)
	content := bar.Content()
	if !strings.Contains(content, "github") {
		t.Error("expected grants at width 80")
	}

	// Narrow: should drop grants first
	bar.SetDimensions(35, 24)
	content = bar.Content()
	stripped := stripANSI(content)
	if strings.Contains(stripped, "github") {
		t.Error("expected grants dropped at width 35")
	}
	if !strings.Contains(content, "run_abc123") {
		t.Error("expected run ID preserved at width 35")
	}

	// Very narrow: should drop name
	bar.SetDimensions(28, 24)
	content = bar.Content()
	stripped = stripANSI(content)
	if strings.Contains(stripped, "my-agent") {
		t.Error("expected name dropped at width 28")
	}

	// Extremely narrow: just spaces
	bar.SetDimensions(5, 24)
	content = bar.Content()
	stripped = stripANSI(content)
	if len([]rune(stripped)) != 5 {
		t.Errorf("expected 5 runes at width 5, got %d", len([]rune(stripped)))
	}
}
