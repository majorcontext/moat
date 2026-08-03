package cli

import "testing"

// The docs promise --tty-trace takes precedence over MOAT_TTY_TRACE; both
// directions are covered so neither the flag nor the fallback can regress
// unnoticed.
func TestResolveTracePath(t *testing.T) {
	tests := []struct {
		name string
		flag string
		env  string
		want string
	}{
		{"neither set disables tracing", "", "", ""},
		{"env alone is used", "", "from-env.tty", "from-env.tty"},
		{"flag alone is used", "from-flag.tty", "", "from-flag.tty"},
		{"flag wins over env", "from-flag.tty", "from-env.tty", "from-flag.tty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MOAT_TTY_TRACE", tt.env)
			if got := resolveTracePath(tt.flag); got != tt.want {
				t.Errorf("resolveTracePath(%q) with MOAT_TTY_TRACE=%q = %q, want %q", tt.flag, tt.env, got, tt.want)
			}
		})
	}
}
