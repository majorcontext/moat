package container

import "testing"

func TestIsRunID(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		// Valid run IDs (8 lowercase hex chars)
		{"a1b2c3d4", true},
		{"00000000", true},
		{"ffffffff", true},
		{"12345678", true},

		// Invalid: wrong length
		{"a1b2c3d", false},   // 7 chars
		{"a1b2c3d4e", false}, // 9 chars
		{"", false},          // empty

		// Invalid: uppercase (our IDs are always lowercase)
		{"A1B2C3D4", false},
		{"a1B2c3d4", false},

		// Invalid: non-hex characters
		{"a1b2c3dg", false}, // 'g' is not hex
		{"a1b2c3d-", false}, // dash
		{"a1b2c3d ", false}, // space
		{"a1b2c3d_", false}, // underscore

		// Not run IDs (common Docker/container names)
		{"myagent", false},
		{"test-bot", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isRunID(tt.input)
			if got != tt.want {
				t.Errorf("isRunID(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
