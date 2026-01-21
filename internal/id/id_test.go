package id

import (
	"regexp"
	"strings"
	"testing"
)

func TestGenerate(t *testing.T) {
	tests := []struct {
		prefix string
	}{
		{"run"},
		{"snap"},
		{"task"},
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			id1 := Generate(tt.prefix)
			id2 := Generate(tt.prefix)

			// Should have correct prefix with underscore
			expectedPrefix := tt.prefix + "_"
			if !strings.HasPrefix(id1, expectedPrefix) {
				t.Errorf("expected prefix %q, got %s", expectedPrefix, id1)
			}

			// Should be unique
			if id1 == id2 {
				t.Errorf("expected unique IDs, got %s and %s", id1, id2)
			}

			// Should have expected length (prefix + underscore + 12 hex chars)
			expectedLen := len(tt.prefix) + 1 + 12
			if len(id1) != expectedLen {
				t.Errorf("expected length %d, got %d (%s)", expectedLen, len(id1), id1)
			}
		})
	}
}

func TestGenerateFormat(t *testing.T) {
	tests := []struct {
		prefix  string
		pattern string
	}{
		{"run", `^run_[0-9a-f]{12}$`},
		{"snap", `^snap_[0-9a-f]{12}$`},
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			id := Generate(tt.prefix)
			matched, err := regexp.MatchString(tt.pattern, id)
			if err != nil {
				t.Fatalf("invalid regex pattern: %v", err)
			}
			if !matched {
				t.Errorf("ID %q doesn't match expected format %s", id, tt.pattern)
			}
		})
	}
}

func TestGenerateUniqueness(t *testing.T) {
	// Generate 1000 IDs and verify no collisions
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := Generate("test")
		if seen[id] {
			t.Errorf("collision detected: %s", id)
		}
		seen[id] = true
	}
}

func TestIsValid(t *testing.T) {
	tests := []struct {
		name   string
		id     string
		prefix string
		want   bool
	}{
		// Valid IDs
		{"valid run ID", "run_abc123def456", "run", true},
		{"valid snap ID", "snap_000000000000", "snap", true},
		{"valid with all digits", "test_012345678901", "test", true},
		{"valid with all letters", "run_abcdefabcdef", "run", true},

		// Invalid prefix
		{"wrong prefix", "run_abc123def456", "snap", false},
		{"missing prefix", "_abc123def456", "run", false},
		{"no underscore", "runabc123def456", "run", false},

		// Invalid suffix length
		{"suffix too short", "run_abc123", "run", false},
		{"suffix too long", "run_abc123def4567", "run", false},
		{"empty suffix", "run_", "run", false},

		// Invalid characters
		{"uppercase hex", "run_ABC123DEF456", "run", false},
		{"non-hex characters", "run_ghijklmnopqr", "run", false},
		{"special characters", "run_abc!23def456", "run", false},
		{"spaces", "run_abc 23def456", "run", false},

		// Edge cases
		{"empty ID", "", "run", false},
		{"just prefix", "run", "run", false},
		{"empty prefix", "run_abc123def456", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValid(tt.id, tt.prefix)
			if got != tt.want {
				t.Errorf("IsValid(%q, %q) = %v, want %v", tt.id, tt.prefix, got, tt.want)
			}
		})
	}
}

func TestIsValidWithGenerate(t *testing.T) {
	// Generated IDs should always be valid
	prefixes := []string{"run", "snap", "task", "test"}
	for _, prefix := range prefixes {
		id := Generate(prefix)
		if !IsValid(id, prefix) {
			t.Errorf("Generated ID %q should be valid for prefix %q", id, prefix)
		}
	}
}
