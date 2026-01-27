// Package audit provides tamper-proof logging with cryptographic verification.
package audit

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/andybons/moat/internal/log"
)

// EntryType identifies the kind of log entry.
type EntryType string

const (
	EntryConsole    EntryType = "console"
	EntryNetwork    EntryType = "network"
	EntryCredential EntryType = "credential"
	EntrySecret     EntryType = "secret"
	EntrySSH        EntryType = "ssh"
	EntryContainer  EntryType = "container"
)

// FirstSequence is the sequence number of the first entry in a log.
// Sequences are 1-indexed to distinguish "no previous entry" (seq=0) from the first entry.
const FirstSequence uint64 = 1

// ConsoleData holds console log entry data.
type ConsoleData struct {
	Line string `json:"line"`
}

// NetworkData holds network request entry data.
type NetworkData struct {
	Method         string `json:"method"`
	URL            string `json:"url"`
	StatusCode     int    `json:"status_code"`
	DurationMs     int64  `json:"duration_ms"`
	CredentialUsed string `json:"credential_used,omitempty"`
	Error          string `json:"error,omitempty"`
}

// CredentialData holds credential usage entry data.
type CredentialData struct {
	Name   string `json:"name"`   // e.g., "github"
	Action string `json:"action"` // e.g., "injected", "used", "revoked"
	Host   string `json:"host"`   // e.g., "api.github.com"
}

// SecretData holds secret resolution entry data.
type SecretData struct {
	Name    string `json:"name"`    // env var name, e.g., "OPENAI_API_KEY"
	Backend string `json:"backend"` // e.g., "1password", "ssm"
	// Note: value is never logged
}

// SSHData holds SSH agent operation entry data.
type SSHData struct {
	Action      string `json:"action"`                // "list", "sign_allowed", "sign_denied"
	Host        string `json:"host,omitempty"`        // target host (for sign operations)
	Fingerprint string `json:"fingerprint,omitempty"` // key fingerprint (for sign operations)
	Error       string `json:"error,omitempty"`       // error message (for denied operations)
}

// ContainerData holds container lifecycle entry data.
type ContainerData struct {
	Action     string `json:"action"`               // "created", "started", "stopped"
	Privileged bool   `json:"privileged,omitempty"` // true if container runs in privileged mode
	Reason     string `json:"reason,omitempty"`     // e.g., "docker:dind" for why privileged

	// BuildKit sidecar info (dind mode only)
	BuildKitEnabled     bool   `json:"buildkit_enabled,omitempty"`
	BuildKitContainerID string `json:"buildkit_container_id,omitempty"`
	BuildKitNetworkID   string `json:"buildkit_network_id,omitempty"`
}

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
	// dataJSON stores the canonical JSON used for hashing. This ensures hash
	// verification works correctly after database round-trips, where Data
	// becomes map[string]any (which marshals with sorted keys, unlike structs).
	dataJSON []byte `json:"-"`
}

// NewEntry creates a new entry with computed hash.
func NewEntry(seq uint64, prevHash string, entryType EntryType, data any) *Entry {
	return newEntryWithTimestamp(seq, prevHash, entryType, data, time.Now().UTC())
}

// newEntryWithTimestamp creates an entry with a specific timestamp (for testing).
// Note: If data fails to marshal, it will be stored as null and logged as a warning.
// Store.Append validates marshaling before calling this, so failures here are rare.
func newEntryWithTimestamp(seq uint64, prevHash string, entryType EntryType, data any, ts time.Time) *Entry {
	dataJSON, err := json.Marshal(data)
	if err != nil {
		log.Warn("failed to marshal entry data", "type", entryType, "error", err)
		dataJSON = []byte("null")
	}
	e := &Entry{
		Sequence:  seq,
		Timestamp: ts,
		Type:      entryType,
		PrevHash:  prevHash,
		Data:      data,
		dataJSON:  dataJSON,
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

	// Data (JSON encoded) - use stored dataJSON if available for consistency
	// after database round-trips where Data becomes map[string]any
	dataBytes := e.dataJSON
	if dataBytes == nil {
		var err error
		dataBytes, err = json.Marshal(e.Data)
		if err != nil {
			log.Warn("failed to marshal entry data for hash", "seq", e.Sequence, "error", err)
			dataBytes = []byte("null")
		}
	}
	h.Write(dataBytes)

	return hex.EncodeToString(h.Sum(nil))
}

// Verify checks if the entry's hash is valid.
func (e *Entry) Verify() bool {
	return e.Hash == e.computeHash()
}

// UnmarshalJSON implements custom JSON unmarshaling to set dataJSON.
// This ensures hash verification works after JSON round-trips.
func (e *Entry) UnmarshalJSON(data []byte) error {
	// Use a type alias to avoid infinite recursion
	type Alias Entry
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(e),
	}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	// Set dataJSON from the unmarshaled Data for hash verification
	var err error
	e.dataJSON, err = json.Marshal(e.Data)
	if err != nil {
		log.Warn("failed to marshal entry data after unmarshal", "seq", e.Sequence, "error", err)
		e.dataJSON = []byte("null")
	}
	return nil
}
