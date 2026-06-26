package container

import "testing"

func TestIsRunID(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		// Valid run IDs: "run_" + 12 lowercase hex chars (id.Generate("run")).
		{"run_a1b2c3d4e5f6", true},
		{"run_000000000000", true},
		{"run_ffffffffffff", true},
		{"run_123456789abc", true},

		// Invalid: wrong hex length
		{"run_a1b2c3d4e5f", false},   // 11 chars
		{"run_a1b2c3d4e5f60", false}, // 13 chars
		{"run_", false},              // no hex
		{"", false},                  // empty

		// Invalid: missing or wrong prefix
		{"a1b2c3d4e5f6", false},      // bare hex, no prefix
		{"a1b2c3d4", false},          // legacy 8-hex form is no longer a run name
		{"snap_a1b2c3d4e5f6", false}, // different prefix

		// Invalid: uppercase (our IDs are always lowercase)
		{"run_A1B2C3D4E5F6", false},
		{"run_a1B2c3d4e5f6", false},

		// Invalid: non-hex characters
		{"run_a1b2c3d4e5fg", false}, // 'g' is not hex
		{"run_a1b2c3d4e5f-", false}, // dash

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

func TestIsValidUsername(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		// Valid usernames
		{"root", true},
		{"node", true},
		{"vscode", true},
		{"moatuser", true},
		{"user1", true},
		{"my_user", true},
		{"my-user", true},
		{"user.name", true},
		{"User123", true},

		// Invalid: empty or too long
		{"", false},
		{"this_username_is_way_too_long_for_posix", false},

		// Invalid: starts with hyphen or dot
		{"-user", false},
		{".user", false},

		// Invalid: path traversal attempts
		{"../etc", false},
		{"user/bin", false},
		{"user\x00name", false},

		// Invalid: special characters
		{"user name", false},
		{"user@host", false},
		{"user:group", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isValidUsername(tt.input)
			if got != tt.want {
				t.Errorf("isValidUsername(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
