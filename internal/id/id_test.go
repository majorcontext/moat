package id

import (
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

			// Should have expected length (prefix + underscore + 8 hex chars)
			expectedLen := len(tt.prefix) + 1 + 8
			if len(id1) != expectedLen {
				t.Errorf("expected length %d, got %d (%s)", expectedLen, len(id1), id1)
			}
		})
	}
}
