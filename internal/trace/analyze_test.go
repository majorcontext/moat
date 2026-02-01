package trace

import (
	"strings"
	"testing"
	"time"
)

func TestDecodeEvent(t *testing.T) {
	tests := []struct {
		name     string
		event    Event
		expected string
	}{
		{
			name: "clear screen",
			event: Event{
				Type: EventStdout,
				Data: []byte("\x1b[2J"),
			},
			expected: "ESC[2J (clear entire screen)",
		},
		{
			name: "cursor position",
			event: Event{
				Type: EventStdout,
				Data: []byte("\x1b[5;10H"),
			},
			expected: "ESC[5;10H (cursor position 5;10)",
		},
		{
			name: "save cursor",
			event: Event{
				Type: EventStdout,
				Data: []byte("\x1b[s"),
			},
			expected: "ESC[s (save cursor)",
		},
		{
			name: "restore cursor",
			event: Event{
				Type: EventStdout,
				Data: []byte("\x1b[u"),
			},
			expected: "ESC[u (restore cursor)",
		},
		{
			name: "resize event",
			event: Event{
				Type: EventResize,
				Size: &Size{Width: 80, Height: 24},
			},
			expected: "RESIZE 80x24",
		},
		{
			name: "text data",
			event: Event{
				Type: EventStdout,
				Data: []byte("Hello, World!"),
			},
			expected: `"Hello, World!"`,
		},
		{
			name: "mixed text and escapes",
			event: Event{
				Type: EventStdout,
				Data: []byte("\x1b[1mBold\x1b[0m"),
			},
			expected: `ESC[1m (SGR: bold) "Bold" ESC[0m (SGR: reset)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecodeEvent(tt.event)
			if got != tt.expected {
				t.Errorf("DecodeEvent() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestFindClearScreen(t *testing.T) {
	trace := &Trace{
		Events: []Event{
			{Type: EventStdout, Data: []byte("Hello")},
			{Type: EventStdout, Data: []byte("\x1b[2J")}, // index 1
			{Type: EventStdout, Data: []byte("World")},
			{Type: EventStdout, Data: []byte("\x1b[3J")}, // index 3
			{Type: EventStdin, Data: []byte("input")},
		},
	}

	indices := trace.FindClearScreen()
	expected := []int{1, 3}

	if len(indices) != len(expected) {
		t.Fatalf("FindClearScreen() found %d clears, want %d", len(indices), len(expected))
	}

	for i, idx := range indices {
		if idx != expected[i] {
			t.Errorf("FindClearScreen()[%d] = %d, want %d", i, idx, expected[i])
		}
	}
}

func TestFindResizeIssues(t *testing.T) {
	trace := &Trace{
		Events: []Event{
			// Resize at 0s
			{TimestampNano: 0, Type: EventResize, Size: &Size{Width: 80, Height: 24}},
			// Clear screen 50ms later (within 100ms window) - should be flagged
			{TimestampNano: 50_000_000, Type: EventStdout, Data: []byte("\x1b[2J")},
			// Another resize at 200ms
			{TimestampNano: 200_000_000, Type: EventResize, Size: &Size{Width: 120, Height: 40}},
			// Clear screen 150ms later (outside 100ms window) - should NOT be flagged
			{TimestampNano: 350_000_000, Type: EventStdout, Data: []byte("\x1b[2J")},
		},
	}

	issues := trace.FindResizeIssues(100 * 1000 * 1000) // 100ms window
	if len(issues) != 1 {
		t.Fatalf("FindResizeIssues() found %d issues, want 1", len(issues))
	}

	// Check that the issue mentions the first resize
	if !strings.Contains(issues[0], "80x24") {
		t.Errorf("Issue should mention 80x24 resize, got: %s", issues[0])
	}
	if !strings.Contains(issues[0], "50") { // 50μs in the output
		t.Errorf("Issue should mention timing, got: %s", issues[0])
	}
}

func TestSaveAndLoad(t *testing.T) {
	tmpFile := t.TempDir() + "/trace.json"

	original := &Trace{
		Metadata: Metadata{
			Timestamp:   time.Now(),
			RunID:       "test123",
			Command:     []string{"claude", "--help"},
			Environment: map[string]string{"TERM": "xterm-256color"},
			InitialSize: Size{Width: 80, Height: 24},
		},
		Events: []Event{
			{TimestampNano: 0, Type: EventStdout, Data: []byte("hello")},
			{TimestampNano: 1000000, Type: EventResize, Size: &Size{Width: 120, Height: 40}},
		},
	}

	// Save
	if err := original.Save(tmpFile); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	// Load
	loaded, err := Load(tmpFile)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Verify
	if loaded.Metadata.RunID != original.Metadata.RunID {
		t.Errorf("RunID = %q, want %q", loaded.Metadata.RunID, original.Metadata.RunID)
	}
	if len(loaded.Events) != len(original.Events) {
		t.Errorf("Events count = %d, want %d", len(loaded.Events), len(original.Events))
	}
}
