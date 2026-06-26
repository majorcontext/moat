package container

import "github.com/majorcontext/moat/internal/id"

// isRunID reports whether s is a moat run ID — the value moat passes as the
// container's `--name`. Run IDs have the form "run_" followed by 12 lowercase
// hex chars (see internal/id.Generate). This is how ListContainers tells moat
// containers apart from unrelated containers on the host.
func isRunID(s string) bool {
	return id.IsValid(s, "run")
}

// isValidUsername checks if a string is a valid POSIX username.
// This prevents path traversal attacks when constructing home directory paths.
// Valid characters: alphanumeric, underscore, hyphen, dot (not starting with hyphen or dot).
func isValidUsername(s string) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	// Cannot start with hyphen or dot
	if s[0] == '-' || s[0] == '.' {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}
