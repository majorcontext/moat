package container

// isRunID checks if a string looks like an moat run ID (8 hex chars).
func isRunID(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
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
