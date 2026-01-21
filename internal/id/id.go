// Package id provides unique identifier generation for moat resources.
package id

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

// Generate creates a unique identifier with the given prefix.
// Format: <prefix>_<12 hex chars> (e.g., "run_abc123def456", "snap_def678abc123")
// Uses 6 cryptographically random bytes encoded as 12 hex characters.
// This provides 2^48 possible IDs (~281 trillion), making collisions extremely unlikely.
func Generate(prefix string) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails (extremely unlikely)
		// Uses nanosecond timestamp to provide reasonable uniqueness
		ts := time.Now().UnixNano()
		fallback := make([]byte, 6)
		for i := 0; i < 6; i++ {
			fallback[i] = byte(ts >> (i * 8))
		}
		return prefix + "_" + hex.EncodeToString(fallback)
	}
	return prefix + "_" + hex.EncodeToString(b)
}

// IsValid checks if an ID has the expected format: <prefix>_<12 hex chars>.
// Returns true if the ID matches the format, false otherwise.
func IsValid(id string, prefix string) bool {
	expectedPrefix := prefix + "_"
	if !strings.HasPrefix(id, expectedPrefix) {
		return false
	}
	suffix := strings.TrimPrefix(id, expectedPrefix)
	if len(suffix) != 12 {
		return false
	}
	// Validate all characters are lowercase hex
	for _, c := range suffix {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
