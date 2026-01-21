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
