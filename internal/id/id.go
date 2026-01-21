// Package id provides unique identifier generation for moat resources.
package id

import (
	"crypto/rand"
	"encoding/hex"
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
