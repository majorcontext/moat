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

func TestStatusBar_MessageOverlay(t *testing.T) {
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetGrants([]string{"github"})
	bar.SetDimensions(80, 24)

	// Normal content should show run info
	normal := bar.Content()
	if !strings.Contains(normal, "run_abc123") {
		t.Errorf("expected run ID in normal content, got %q", normal)
	}
	if !strings.Contains(normal, "my-agent") {
		t.Errorf("expected agent name in normal content, got %q", normal)
	}

	// Set message overlay
	bar.SetMessage("Escape: d (detach) · k (stop)")
	message := bar.Content()

	// Should show message, not normal content
	if !strings.Contains(message, "Escape") {
		t.Errorf("expected message text in overlay, got %q", message)
	}
	if !strings.Contains(message, "detach") {
		t.Errorf("expected 'detach' in message, got %q", message)
	}
	if strings.Contains(message, "run_abc123") {
		t.Errorf("expected run ID hidden when message is set, got %q", message)
	}
	if strings.Contains(message, "my-agent") {
		t.Errorf("expected agent name hidden when message is set, got %q", message)
	}

	// Clear message
	bar.ClearMessage()
	restored := bar.Content()

	// Should restore normal content
	if !strings.Contains(restored, "run_abc123") {
		t.Errorf("expected run ID restored after clearing message, got %q", restored)
	}
	if strings.Contains(restored, "Escape") {
		t.Errorf("expected message cleared, got %q", restored)
	}
}

func TestStatusBar_MessageOverlay_Width(t *testing.T) {
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(40, 24)

	// Set a message
	bar.SetMessage("Press d to detach or k to stop")
	content := bar.Content()

	// Message should be centered and fit width
	stripped := stripANSI(content)
	runeCount := len([]rune(stripped))
	if runeCount != 40 {
		t.Errorf("expected message width = 40 runes, got %d: %q", runeCount, stripped)
	}
}

func TestStatusBar_MessageOverlay_LongMessage(t *testing.T) {
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetDimensions(30, 24)

	// Set a message longer than width
	longMsg := "This is a very long message that should be truncated to fit"
	bar.SetMessage(longMsg)
	content := bar.Content()

	// Should be truncated to fit
	stripped := stripANSI(content)
	runeCount := len([]rune(stripped))
	if runeCount != 30 {
		t.Errorf("expected truncated message width = 30 runes, got %d: %q", runeCount, stripped)
	}
}

func TestStatusBar_CtrlSlashHint(t *testing.T) {
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetGrants([]string{"github"})
	bar.SetDimensions(80, 24)

	content := bar.Content()
	stripped := stripANSI(content)

	// Should show ctrl+/ hint
	if !strings.Contains(stripped, "(ctrl+/)") {
		t.Errorf("expected (ctrl+/) hint in content, got: %q", stripped)
	}

	// Should still show all the normal content
	if !strings.Contains(content, "run_abc123") {
		t.Errorf("expected run ID, got: %q", stripped)
	}
	if !strings.Contains(content, "my-agent") {
		t.Errorf("expected agent name, got: %q", stripped)
	}
	if !strings.Contains(content, "github") {
		t.Errorf("expected grants, got: %q", stripped)
	}

	// Hint should be dimmed
	if !strings.Contains(content, "\x1b[2m") {
		t.Errorf("expected dim style for hint, got: %q", content)
	}

	// Print for visual inspection
	t.Logf("Full width (80): %q", stripped)
}

func TestStatusBar_CtrlSlashHint_TruncationCascade(t *testing.T) {
	bar := NewStatusBar("run_abc123", "my-agent", "docker")
	bar.SetGrants([]string{"github", "aws"})

	tests := []struct {
		width    int
		wantHint bool
		name     string
	}{
		{80, true, "wide: hint visible"},
		{60, true, "medium: hint visible"},
		{45, false, "narrow: hint dropped"},
		{35, false, "narrower: hint and grants dropped"},
		{30, false, "very narrow: only run ID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bar.SetDimensions(tt.width, 24)
			content := bar.Content()
			stripped := stripANSI(content)

			hasHint := strings.Contains(stripped, "(ctrl+/)")
			if hasHint != tt.wantHint {
				t.Errorf("width %d: hasHint=%v, want %v. Content: %q",
					tt.width, hasHint, tt.wantHint, stripped)
			}

			// Verify width constraint
			runeCount := len([]rune(stripped))
			if runeCount != tt.width {
				t.Errorf("width %d: got %d runes, want %d", tt.width, runeCount, tt.width)
			}

			t.Logf("Width %d: %q", tt.width, stripped)
		})
	}
}
