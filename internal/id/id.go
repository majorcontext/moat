// Package id provides unique identifier generation for moat resources.
package id

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Generate creates a unique identifier with the given prefix.
// Format: <prefix>_<8 hex chars> (e.g., "run_abc12345", "snap_def67890")
// Uses 4 cryptographically random bytes encoded as 8 hex characters.
func Generate(prefix string) string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails (extremely unlikely)
		return prefix + "_" + hex.EncodeToString([]byte(time.Now().Format("150405.0")))[:8]
	}
	return prefix + "_" + hex.EncodeToString(b)
}
