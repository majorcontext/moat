// Package audit provides tamper-proof logging with cryptographic verification.
package audit

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"time"
)

// EntryType identifies the kind of log entry.
type EntryType string

const (
	EntryConsole    EntryType = "console"
	EntryNetwork    EntryType = "network"
	EntryCredential EntryType = "credential"
)

// Entry represents a single hash-chained log entry.
type Entry struct {
	Sequence  uint64    `json:"seq"`
	Timestamp time.Time `json:"ts"`
	Type      EntryType `json:"type"`
	PrevHash  string    `json:"prev"`
	// Data must be JSON-serializable. Non-serializable values (e.g., channels,
	// functions, cycles) will marshal as null, which may cause hash collisions.
	Data any    `json:"data"`
	Hash string `json:"hash"`
}

// NewEntry creates a new entry with computed hash.
func NewEntry(seq uint64, prevHash string, entryType EntryType, data any) *Entry {
	return newEntryWithTimestamp(seq, prevHash, entryType, data, time.Now().UTC())
}

// newEntryWithTimestamp creates an entry with a specific timestamp (for testing).
func newEntryWithTimestamp(seq uint64, prevHash string, entryType EntryType, data any, ts time.Time) *Entry {
	e := &Entry{
		Sequence:  seq,
		Timestamp: ts,
		Type:      entryType,
		PrevHash:  prevHash,
		Data:      data,
	}
	e.Hash = e.computeHash()
	return e
}

// computeHash calculates SHA-256(seq || ts || type || prev || data).
func (e *Entry) computeHash() string {
	h := sha256.New()

	// Sequence (8 bytes, big endian)
	seqBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(seqBytes, e.Sequence)
	h.Write(seqBytes)

	// Timestamp (RFC3339)
	h.Write([]byte(e.Timestamp.Format(time.RFC3339Nano)))

	// Type
	h.Write([]byte(e.Type))

	// PrevHash
	h.Write([]byte(e.PrevHash))

	// Data (JSON encoded)
	dataBytes, _ := json.Marshal(e.Data)
	h.Write(dataBytes)

	return hex.EncodeToString(h.Sum(nil))
}

// Verify checks if the entry's hash is valid.
func (e *Entry) Verify() bool {
	return e.Hash == e.computeHash()
}
