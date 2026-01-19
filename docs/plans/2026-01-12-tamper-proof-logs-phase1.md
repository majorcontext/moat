# Tamper-Proof Logs Phase 1: Hash Chain + SQLite Storage

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Create a cryptographically verifiable logging system with hash-chained entries stored in SQLite.

**Architecture:** New `internal/audit` package with hash-chained log entries. Each entry contains sequence number, timestamp, type, previous hash, payload, and self-hash. SQLite provides atomic transactions and efficient queries. The log collector accepts entries via runtime-aware transport (Unix socket for Docker, TCP+token for Apple containers).

**Tech Stack:** Pure Go SQLite (`modernc.org/sqlite`), SHA-256 hashing, Unix sockets, TCP with token auth.

**Design Doc:** `docs/plans/2026-01-12-tamper-proof-logs-design.md`

---

## Task 1: Create Audit Package with Entry Types

**Files:**
- Create: `internal/audit/entry.go`
- Create: `internal/audit/entry_test.go`

**Step 1: Write the failing test**

Create `internal/audit/entry_test.go`:

```go
package audit

import (
	"testing"
	"time"
)

func TestNewEntry_AssignsSequenceAndHash(t *testing.T) {
	e := NewEntry(1, "", EntryConsole, map[string]any{"line": "hello"})

	if e.Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", e.Sequence)
	}
	if e.Type != EntryConsole {
		t.Errorf("Type = %s, want %s", e.Type, EntryConsole)
	}
	if e.Hash == "" {
		t.Error("Hash should not be empty")
	}
	if e.PrevHash != "" {
		t.Error("PrevHash should be empty for first entry")
	}
}

func TestNewEntry_IncludesPrevHash(t *testing.T) {
	e1 := NewEntry(1, "", EntryConsole, map[string]any{"line": "first"})
	e2 := NewEntry(2, e1.Hash, EntryConsole, map[string]any{"line": "second"})

	if e2.PrevHash != e1.Hash {
		t.Errorf("PrevHash = %s, want %s", e2.PrevHash, e1.Hash)
	}
}

func TestEntry_HashIsConsistent(t *testing.T) {
	ts := time.Date(2026, 1, 12, 10, 0, 0, 0, time.UTC)
	data := map[string]any{"line": "test"}

	e1 := newEntryWithTimestamp(1, "", EntryConsole, data, ts)
	e2 := newEntryWithTimestamp(1, "", EntryConsole, data, ts)

	if e1.Hash != e2.Hash {
		t.Errorf("Hashes should be identical for same inputs")
	}
}

func TestEntry_HashChangesWithSequence(t *testing.T) {
	ts := time.Date(2026, 1, 12, 10, 0, 0, 0, time.UTC)
	data := map[string]any{"line": "test"}

	e1 := newEntryWithTimestamp(1, "", EntryConsole, data, ts)
	e2 := newEntryWithTimestamp(2, "", EntryConsole, data, ts)

	if e1.Hash == e2.Hash {
		t.Error("Different sequences should produce different hashes")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
go test ./internal/audit/... -v
```

Expected: FAIL - package does not exist

**Step 3: Write minimal implementation**

Create `internal/audit/entry.go`:

```go
// Package audit provides tamper-proof logging with cryptographic verification.
package audit

import (
	"crypto/sha256"
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
	Data      any       `json:"data"`
	Hash      string    `json:"hash"`
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
	for i := 7; i >= 0; i-- {
		seqBytes[i] = byte(e.Sequence)
		e.Sequence >>= 8
	}
	// Restore sequence
	for i := 0; i < 8; i++ {
		e.Sequence = (e.Sequence << 8) | uint64(seqBytes[i])
	}
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
```

**Step 4: Run test to verify it passes**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
go test ./internal/audit/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/audit/
git commit -m "feat(audit): add hash-chained entry type with SHA-256 verification"
```

---

## Task 2: Add Entry Verification Test

**Files:**
- Modify: `internal/audit/entry_test.go`

**Step 1: Write the failing test**

Add to `internal/audit/entry_test.go`:

```go
func TestEntry_Verify_ValidEntry(t *testing.T) {
	e := NewEntry(1, "", EntryConsole, map[string]any{"line": "test"})

	if !e.Verify() {
		t.Error("Valid entry should verify")
	}
}

func TestEntry_Verify_TamperedData(t *testing.T) {
	e := NewEntry(1, "", EntryConsole, map[string]any{"line": "test"})

	// Tamper with data
	e.Data = map[string]any{"line": "tampered"}

	if e.Verify() {
		t.Error("Tampered entry should not verify")
	}
}

func TestEntry_Verify_TamperedSequence(t *testing.T) {
	e := NewEntry(1, "", EntryConsole, map[string]any{"line": "test"})

	// Tamper with sequence
	e.Sequence = 999

	if e.Verify() {
		t.Error("Tampered sequence should not verify")
	}
}
```

**Step 2: Run test to verify it passes**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
go test ./internal/audit/... -v
```

Expected: PASS (Verify already implemented)

**Step 3: Commit**

```bash
git add internal/audit/entry_test.go
git commit -m "test(audit): add verification tests for tamper detection"
```

---

## Task 3: Create SQLite Store

**Files:**
- Create: `internal/audit/store.go`
- Create: `internal/audit/store_test.go`

**Step 1: Add SQLite dependency**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
go get modernc.org/sqlite
```

**Step 2: Write the failing test**

Create `internal/audit/store_test.go`:

```go
package audit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore_OpenClose(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "logs.db")

	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	// Verify file was created
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("Database file not created: %v", err)
	}
}

func TestStore_Append_FirstEntry(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	entry, err := store.Append(EntryConsole, map[string]any{"line": "hello"})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	if entry.Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", entry.Sequence)
	}
	if entry.PrevHash != "" {
		t.Errorf("PrevHash = %q, want empty for first entry", entry.PrevHash)
	}
	if entry.Hash == "" {
		t.Error("Hash should not be empty")
	}
}

func TestStore_Append_ChainedEntries(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	e1, _ := store.Append(EntryConsole, map[string]any{"line": "first"})
	e2, _ := store.Append(EntryConsole, map[string]any{"line": "second"})
	e3, _ := store.Append(EntryNetwork, map[string]any{"url": "https://api.github.com"})

	if e2.PrevHash != e1.Hash {
		t.Errorf("e2.PrevHash = %s, want %s", e2.PrevHash, e1.Hash)
	}
	if e3.PrevHash != e2.Hash {
		t.Errorf("e3.PrevHash = %s, want %s", e3.PrevHash, e2.Hash)
	}
	if e3.Sequence != 3 {
		t.Errorf("e3.Sequence = %d, want 3", e3.Sequence)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "logs.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	return store
}
```

**Step 3: Run test to verify it fails**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
go test ./internal/audit/... -v
```

Expected: FAIL - Store type does not exist

**Step 4: Write minimal implementation**

Create `internal/audit/store.go`:

```go
package audit

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Store provides tamper-proof log storage using SQLite.
type Store struct {
	db       *sql.DB
	mu       sync.Mutex
	lastHash string
	lastSeq  uint64
}

// OpenStore opens or creates a log store at the given path.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Create tables
	if err := createTables(db); err != nil {
		db.Close()
		return nil, err
	}

	// Load last entry state
	store := &Store{db: db}
	if err := store.loadLastEntry(); err != nil {
		db.Close()
		return nil, err
	}

	return store, nil
}

func createTables(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS entries (
			seq       INTEGER PRIMARY KEY,
			ts        TEXT NOT NULL,
			type      TEXT NOT NULL,
			prev_hash TEXT NOT NULL,
			data      TEXT NOT NULL,
			hash      TEXT NOT NULL UNIQUE
		);
		CREATE INDEX IF NOT EXISTS idx_entries_type ON entries(type);
		CREATE INDEX IF NOT EXISTS idx_entries_ts ON entries(ts);
	`)
	return err
}

func (s *Store) loadLastEntry() error {
	row := s.db.QueryRow(`
		SELECT seq, hash FROM entries ORDER BY seq DESC LIMIT 1
	`)
	var seq uint64
	var hash string
	err := row.Scan(&seq, &hash)
	if err == sql.ErrNoRows {
		return nil // Empty store
	}
	if err != nil {
		return fmt.Errorf("loading last entry: %w", err)
	}
	s.lastSeq = seq
	s.lastHash = hash
	return nil
}

// Append adds a new entry to the store, returning the created entry.
func (s *Store) Append(entryType EntryType, data any) (*Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := NewEntry(s.lastSeq+1, s.lastHash, entryType, data)

	dataJSON, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshaling data: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO entries (seq, ts, type, prev_hash, data, hash)
		VALUES (?, ?, ?, ?, ?, ?)
	`, entry.Sequence, entry.Timestamp.Format(time.RFC3339Nano),
		entry.Type, entry.PrevHash, string(dataJSON), entry.Hash)
	if err != nil {
		return nil, fmt.Errorf("inserting entry: %w", err)
	}

	s.lastSeq = entry.Sequence
	s.lastHash = entry.Hash

	return entry, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}
```

**Step 5: Run test to verify it passes**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
go test ./internal/audit/... -v
```

Expected: PASS

**Step 6: Commit**

```bash
git add internal/audit/store.go internal/audit/store_test.go go.mod go.sum
git commit -m "feat(audit): add SQLite store with hash chain append"
```

---

## Task 4: Add Store Query Methods

**Files:**
- Modify: `internal/audit/store.go`
- Modify: `internal/audit/store_test.go`

**Step 1: Write the failing tests**

Add to `internal/audit/store_test.go`:

```go
func TestStore_Get(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	original, _ := store.Append(EntryConsole, map[string]any{"line": "test"})

	retrieved, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if retrieved.Hash != original.Hash {
		t.Errorf("Hash = %s, want %s", retrieved.Hash, original.Hash)
	}
}

func TestStore_Get_NotFound(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	_, err := store.Get(999)
	if err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestStore_Count(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	if count := store.Count(); count != 0 {
		t.Errorf("Count = %d, want 0", count)
	}

	store.Append(EntryConsole, map[string]any{"line": "1"})
	store.Append(EntryConsole, map[string]any{"line": "2"})
	store.Append(EntryNetwork, map[string]any{"url": "http://example.com"})

	if count := store.Count(); count != 3 {
		t.Errorf("Count = %d, want 3", count)
	}
}

func TestStore_Range(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	for i := 0; i < 10; i++ {
		store.Append(EntryConsole, map[string]any{"line": i})
	}

	entries, err := store.Range(3, 7)
	if err != nil {
		t.Fatalf("Range: %v", err)
	}

	if len(entries) != 5 {
		t.Errorf("len(entries) = %d, want 5", len(entries))
	}
	if entries[0].Sequence != 3 {
		t.Errorf("First entry seq = %d, want 3", entries[0].Sequence)
	}
	if entries[4].Sequence != 7 {
		t.Errorf("Last entry seq = %d, want 7", entries[4].Sequence)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
go test ./internal/audit/... -v
```

Expected: FAIL - Get, Count, Range methods don't exist

**Step 3: Write minimal implementation**

Add to `internal/audit/store.go`:

```go
import "errors"

// ErrNotFound is returned when an entry doesn't exist.
var ErrNotFound = errors.New("entry not found")

// Get retrieves an entry by sequence number.
func (s *Store) Get(seq uint64) (*Entry, error) {
	row := s.db.QueryRow(`
		SELECT seq, ts, type, prev_hash, data, hash
		FROM entries WHERE seq = ?
	`, seq)

	return scanEntry(row)
}

// Count returns the total number of entries.
func (s *Store) Count() uint64 {
	var count uint64
	s.db.QueryRow(`SELECT COUNT(*) FROM entries`).Scan(&count)
	return count
}

// Range retrieves entries from startSeq to endSeq (inclusive).
func (s *Store) Range(startSeq, endSeq uint64) ([]*Entry, error) {
	rows, err := s.db.Query(`
		SELECT seq, ts, type, prev_hash, data, hash
		FROM entries WHERE seq >= ? AND seq <= ?
		ORDER BY seq
	`, startSeq, endSeq)
	if err != nil {
		return nil, fmt.Errorf("querying range: %w", err)
	}
	defer rows.Close()

	var entries []*Entry
	for rows.Next() {
		e, err := scanEntryRows(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanEntry(row *sql.Row) (*Entry, error) {
	var e Entry
	var tsStr, dataStr string
	err := row.Scan(&e.Sequence, &tsStr, &e.Type, &e.PrevHash, &dataStr, &e.Hash)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning entry: %w", err)
	}

	e.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
	json.Unmarshal([]byte(dataStr), &e.Data)
	return &e, nil
}

func scanEntryRows(rows *sql.Rows) (*Entry, error) {
	var e Entry
	var tsStr, dataStr string
	err := rows.Scan(&e.Sequence, &tsStr, &e.Type, &e.PrevHash, &dataStr, &e.Hash)
	if err != nil {
		return nil, fmt.Errorf("scanning entry: %w", err)
	}

	e.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
	json.Unmarshal([]byte(dataStr), &e.Data)
	return &e, nil
}
```

**Step 4: Run test to verify it passes**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
go test ./internal/audit/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/audit/store.go internal/audit/store_test.go
git commit -m "feat(audit): add Get, Count, Range query methods to store"
```

---

## Task 5: Add Hash Chain Verification

**Files:**
- Modify: `internal/audit/store.go`
- Modify: `internal/audit/store_test.go`

**Step 1: Write the failing tests**

Add to `internal/audit/store_test.go`:

```go
func TestStore_VerifyChain_Empty(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	result, err := store.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !result.Valid {
		t.Error("Empty chain should be valid")
	}
}

func TestStore_VerifyChain_Valid(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	for i := 0; i < 100; i++ {
		store.Append(EntryConsole, map[string]any{"line": i})
	}

	result, err := store.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !result.Valid {
		t.Errorf("Valid chain should verify: %s", result.Error)
	}
	if result.EntryCount != 100 {
		t.Errorf("EntryCount = %d, want 100", result.EntryCount)
	}
}

func TestStore_VerifyChain_TamperedEntry(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	for i := 0; i < 10; i++ {
		store.Append(EntryConsole, map[string]any{"line": i})
	}

	// Directly tamper with entry 5 in the database
	store.db.Exec(`UPDATE entries SET data = '{"line": "TAMPERED"}' WHERE seq = 5`)

	result, err := store.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if result.Valid {
		t.Error("Tampered chain should not verify")
	}
	if result.FirstInvalidSeq != 5 {
		t.Errorf("FirstInvalidSeq = %d, want 5", result.FirstInvalidSeq)
	}
}

func TestStore_VerifyChain_BrokenLink(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	for i := 0; i < 10; i++ {
		store.Append(EntryConsole, map[string]any{"line": i})
	}

	// Break the chain by modifying prev_hash
	store.db.Exec(`UPDATE entries SET prev_hash = 'wrong' WHERE seq = 5`)

	result, err := store.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if result.Valid {
		t.Error("Broken chain should not verify")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
go test ./internal/audit/... -v
```

Expected: FAIL - VerifyChain method doesn't exist

**Step 3: Write minimal implementation**

Add to `internal/audit/store.go`:

```go
// VerifyResult contains the result of chain verification.
type VerifyResult struct {
	Valid           bool
	EntryCount      uint64
	FirstInvalidSeq uint64
	Error           string
}

// VerifyChain verifies the integrity of the entire hash chain.
func (s *Store) VerifyChain() (*VerifyResult, error) {
	rows, err := s.db.Query(`
		SELECT seq, ts, type, prev_hash, data, hash
		FROM entries ORDER BY seq
	`)
	if err != nil {
		return nil, fmt.Errorf("querying entries: %w", err)
	}
	defer rows.Close()

	result := &VerifyResult{Valid: true}
	var prevHash string
	var prevSeq uint64

	for rows.Next() {
		e, err := scanEntryRows(rows)
		if err != nil {
			return nil, err
		}
		result.EntryCount++

		// Check sequence is monotonic with no gaps
		if prevSeq > 0 && e.Sequence != prevSeq+1 {
			result.Valid = false
			result.FirstInvalidSeq = e.Sequence
			result.Error = fmt.Sprintf("sequence gap: expected %d, got %d", prevSeq+1, e.Sequence)
			return result, nil
		}

		// Check prev_hash links correctly
		if e.PrevHash != prevHash {
			result.Valid = false
			result.FirstInvalidSeq = e.Sequence
			result.Error = fmt.Sprintf("broken chain at seq %d: prev_hash mismatch", e.Sequence)
			return result, nil
		}

		// Verify entry hash
		if !e.Verify() {
			result.Valid = false
			result.FirstInvalidSeq = e.Sequence
			result.Error = fmt.Sprintf("invalid hash at seq %d: entry tampered", e.Sequence)
			return result, nil
		}

		prevHash = e.Hash
		prevSeq = e.Sequence
	}

	return result, rows.Err()
}
```

**Step 4: Run test to verify it passes**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
go test ./internal/audit/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/audit/store.go internal/audit/store_test.go
git commit -m "feat(audit): add hash chain verification with tamper detection"
```

---

## Task 6: Create Log Collector - Unix Socket Transport

**Files:**
- Create: `internal/audit/collector.go`
- Create: `internal/audit/collector_test.go`

**Step 1: Write the failing test**

Create `internal/audit/collector_test.go`:

```go
package audit

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestCollector_UnixSocket_AcceptsWrites(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "logs.db"))
	defer store.Close()

	collector := NewCollector(store)
	socketPath := filepath.Join(dir, "log.sock")

	if err := collector.StartUnix(socketPath); err != nil {
		t.Fatalf("StartUnix: %v", err)
	}
	defer collector.Stop()

	// Connect as client
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Send a log message
	msg := CollectorMessage{
		Type: string(EntryConsole),
		Data: map[string]any{"line": "hello from agent"},
	}
	json.NewEncoder(conn).Encode(msg)

	// Give collector time to process
	time.Sleep(50 * time.Millisecond)

	// Verify entry was stored
	if count := store.Count(); count != 1 {
		t.Errorf("Count = %d, want 1", count)
	}

	entry, _ := store.Get(1)
	data := entry.Data.(map[string]any)
	if data["line"] != "hello from agent" {
		t.Errorf("line = %v, want 'hello from agent'", data["line"])
	}
}

func TestCollector_UnixSocket_MultipleMessages(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "logs.db"))
	defer store.Close()

	collector := NewCollector(store)
	socketPath := filepath.Join(dir, "log.sock")
	collector.StartUnix(socketPath)
	defer collector.Stop()

	conn, _ := net.Dial("unix", socketPath)
	defer conn.Close()

	// Send multiple messages
	for i := 0; i < 10; i++ {
		msg := CollectorMessage{
			Type: string(EntryConsole),
			Data: map[string]any{"line": i},
		}
		json.NewEncoder(conn).Encode(msg)
	}

	time.Sleep(100 * time.Millisecond)

	if count := store.Count(); count != 10 {
		t.Errorf("Count = %d, want 10", count)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
go test ./internal/audit/... -v
```

Expected: FAIL - Collector type doesn't exist

**Step 3: Write minimal implementation**

Create `internal/audit/collector.go`:

```go
package audit

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"sync"
)

// CollectorMessage is the wire format for log messages from agents.
type CollectorMessage struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Collector receives log messages and stores them with hash chaining.
type Collector struct {
	store    *Store
	listener net.Listener
	done     chan struct{}
	wg       sync.WaitGroup
}

// NewCollector creates a new log collector.
func NewCollector(store *Store) *Collector {
	return &Collector{
		store: store,
		done:  make(chan struct{}),
	}
}

// StartUnix starts the collector listening on a Unix socket.
func (c *Collector) StartUnix(socketPath string) error {
	// Remove existing socket file
	os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	c.listener = listener

	// Set write-only permissions (0222)
	os.Chmod(socketPath, 0222)

	c.wg.Add(1)
	go c.acceptLoop()

	return nil
}

func (c *Collector) acceptLoop() {
	defer c.wg.Done()

	for {
		conn, err := c.listener.Accept()
		if err != nil {
			select {
			case <-c.done:
				return
			default:
				continue
			}
		}

		c.wg.Add(1)
		go c.handleConnection(conn)
	}
}

func (c *Collector) handleConnection(conn net.Conn) {
	defer c.wg.Done()
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var msg CollectorMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		entryType := EntryType(msg.Type)
		if entryType == "" {
			entryType = EntryConsole
		}

		c.store.Append(entryType, msg.Data)
	}
}

// Stop stops the collector and waits for all connections to close.
func (c *Collector) Stop() error {
	close(c.done)
	if c.listener != nil {
		c.listener.Close()
	}
	c.wg.Wait()
	return nil
}
```

**Step 4: Run test to verify it passes**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
go test ./internal/audit/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/audit/collector.go internal/audit/collector_test.go
git commit -m "feat(audit): add log collector with Unix socket transport"
```

---

## Task 7: Add TCP Transport with Token Authentication

**Files:**
- Modify: `internal/audit/collector.go`
- Modify: `internal/audit/collector_test.go`

**Step 1: Write the failing tests**

Add to `internal/audit/collector_test.go`:

```go
func TestCollector_TCP_RequiresAuth(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "logs.db"))
	defer store.Close()

	collector := NewCollector(store)
	token := "secret-token-12345678901234567890123456789012"

	port, err := collector.StartTCP(token)
	if err != nil {
		t.Fatalf("StartTCP: %v", err)
	}
	defer collector.Stop()

	// Connect without auth
	conn, _ := net.Dial("tcp", "127.0.0.1:"+port)
	defer conn.Close()

	// Send message without auth - should be rejected
	msg := CollectorMessage{
		Type: string(EntryConsole),
		Data: map[string]any{"line": "unauthorized"},
	}
	json.NewEncoder(conn).Encode(msg)

	time.Sleep(50 * time.Millisecond)

	// Should not be stored
	if count := store.Count(); count != 0 {
		t.Errorf("Count = %d, want 0 (unauthorized)", count)
	}
}

func TestCollector_TCP_AcceptsAuthenticatedWrites(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "logs.db"))
	defer store.Close()

	collector := NewCollector(store)
	token := "secret-token-12345678901234567890123456789012"

	port, err := collector.StartTCP(token)
	if err != nil {
		t.Fatalf("StartTCP: %v", err)
	}
	defer collector.Stop()

	conn, _ := net.Dial("tcp", "127.0.0.1:"+port)
	defer conn.Close()

	// Send auth token first (64 hex chars)
	conn.Write([]byte(token))

	// Now send message
	msg := CollectorMessage{
		Type: string(EntryConsole),
		Data: map[string]any{"line": "authenticated"},
	}
	json.NewEncoder(conn).Encode(msg)

	time.Sleep(50 * time.Millisecond)

	if count := store.Count(); count != 1 {
		t.Errorf("Count = %d, want 1", count)
	}
}

func TestCollector_TCP_RejectsWrongToken(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "logs.db"))
	defer store.Close()

	collector := NewCollector(store)
	token := "secret-token-12345678901234567890123456789012"

	port, _ := collector.StartTCP(token)
	defer collector.Stop()

	conn, _ := net.Dial("tcp", "127.0.0.1:"+port)
	defer conn.Close()

	// Send wrong token
	conn.Write([]byte("wrong-token-123456789012345678901234567890"))

	msg := CollectorMessage{
		Type: string(EntryConsole),
		Data: map[string]any{"line": "should reject"},
	}
	json.NewEncoder(conn).Encode(msg)

	time.Sleep(50 * time.Millisecond)

	if count := store.Count(); count != 0 {
		t.Errorf("Count = %d, want 0 (wrong token)", count)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
go test ./internal/audit/... -v
```

Expected: FAIL - StartTCP method doesn't exist

**Step 3: Write minimal implementation**

Add to `internal/audit/collector.go`:

```go
import (
	"crypto/subtle"
	"fmt"
	"io"
)

// StartTCP starts the collector listening on TCP with token authentication.
// Returns the port number the server is listening on.
func (c *Collector) StartTCP(authToken string) (string, error) {
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return "", err
	}
	c.listener = listener
	c.authToken = authToken

	port := fmt.Sprintf("%d", listener.Addr().(*net.TCPAddr).Port)

	c.wg.Add(1)
	go c.acceptLoop()

	return port, nil
}

// Add authToken field to Collector struct
type Collector struct {
	store     *Store
	listener  net.Listener
	authToken string // For TCP transport
	done      chan struct{}
	wg        sync.WaitGroup
}

// Update handleConnection to check auth for TCP
func (c *Collector) handleConnection(conn net.Conn) {
	defer c.wg.Done()
	defer conn.Close()

	// If auth token is set (TCP mode), require authentication
	if c.authToken != "" {
		token := make([]byte, len(c.authToken))
		if _, err := io.ReadFull(conn, token); err != nil {
			return // Connection closed or incomplete token
		}
		if subtle.ConstantTimeCompare(token, []byte(c.authToken)) != 1 {
			return // Wrong token - close connection
		}
	}

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var msg CollectorMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		entryType := EntryType(msg.Type)
		if entryType == "" {
			entryType = EntryConsole
		}

		c.store.Append(entryType, msg.Data)
	}
}
```

Note: The Collector struct needs to be updated to include authToken. The full updated struct and handleConnection method should replace the existing ones.

**Step 4: Run test to verify it passes**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
go test ./internal/audit/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/audit/collector.go internal/audit/collector_test.go
git commit -m "feat(audit): add TCP transport with token authentication"
```

---

## Task 8: Add Typed Entry Helpers

**Files:**
- Modify: `internal/audit/entry.go`
- Modify: `internal/audit/store.go`
- Modify: `internal/audit/store_test.go`

**Step 1: Write the failing tests**

Add to `internal/audit/store_test.go`:

```go
func TestStore_AppendConsole(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	entry, err := store.AppendConsole("hello world")
	if err != nil {
		t.Fatalf("AppendConsole: %v", err)
	}

	if entry.Type != EntryConsole {
		t.Errorf("Type = %s, want %s", entry.Type, EntryConsole)
	}

	data := entry.Data.(*ConsoleData)
	if data.Line != "hello world" {
		t.Errorf("Line = %s, want 'hello world'", data.Line)
	}
}

func TestStore_AppendNetwork(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	entry, err := store.AppendNetwork(NetworkData{
		Method:         "GET",
		URL:            "https://api.github.com/user",
		StatusCode:     200,
		DurationMs:     150,
		CredentialUsed: "github",
	})
	if err != nil {
		t.Fatalf("AppendNetwork: %v", err)
	}

	if entry.Type != EntryNetwork {
		t.Errorf("Type = %s, want %s", entry.Type, EntryNetwork)
	}

	data := entry.Data.(*NetworkData)
	if data.URL != "https://api.github.com/user" {
		t.Errorf("URL = %s, want 'https://api.github.com/user'", data.URL)
	}
}

func TestStore_AppendCredential(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	entry, err := store.AppendCredential(CredentialData{
		Name:   "github",
		Action: "injected",
		Host:   "api.github.com",
	})
	if err != nil {
		t.Fatalf("AppendCredential: %v", err)
	}

	if entry.Type != EntryCredential {
		t.Errorf("Type = %s, want %s", entry.Type, EntryCredential)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
go test ./internal/audit/... -v
```

Expected: FAIL - typed data structs and methods don't exist

**Step 3: Write minimal implementation**

Add to `internal/audit/entry.go`:

```go
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
```

Add to `internal/audit/store.go`:

```go
// AppendConsole adds a console log entry.
func (s *Store) AppendConsole(line string) (*Entry, error) {
	return s.Append(EntryConsole, &ConsoleData{Line: line})
}

// AppendNetwork adds a network request entry.
func (s *Store) AppendNetwork(data NetworkData) (*Entry, error) {
	return s.Append(EntryNetwork, &data)
}

// AppendCredential adds a credential usage entry.
func (s *Store) AppendCredential(data CredentialData) (*Entry, error) {
	return s.Append(EntryCredential, &data)
}
```

**Step 4: Run test to verify it passes**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
go test ./internal/audit/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/audit/
git commit -m "feat(audit): add typed entry helpers for console, network, credential"
```

---

## Task 9: Integration Test - Full Workflow

**Files:**
- Create: `internal/audit/integration_test.go`

**Step 1: Write the integration test**

Create `internal/audit/integration_test.go`:

```go
package audit

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestIntegration_FullWorkflow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "logs.db")

	// 1. Create store
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	// 2. Start collector
	collector := NewCollector(store)
	socketPath := filepath.Join(dir, "log.sock")
	if err := collector.StartUnix(socketPath); err != nil {
		t.Fatalf("StartUnix: %v", err)
	}

	// 3. Simulate agent writing logs
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	// Write console logs
	for i := 0; i < 5; i++ {
		msg := CollectorMessage{Type: "console", Data: map[string]any{"line": i}}
		json.NewEncoder(conn).Encode(msg)
	}

	// Write network request
	msg := CollectorMessage{
		Type: "network",
		Data: map[string]any{
			"method":      "GET",
			"url":         "https://api.github.com/user",
			"status_code": 200,
			"duration_ms": 150,
		},
	}
	json.NewEncoder(conn).Encode(msg)

	// Write credential event
	msg = CollectorMessage{
		Type: "credential",
		Data: map[string]any{
			"name":   "github",
			"action": "injected",
			"host":   "api.github.com",
		},
	}
	json.NewEncoder(conn).Encode(msg)

	conn.Close()
	time.Sleep(100 * time.Millisecond)

	// 4. Stop collector
	collector.Stop()

	// 5. Verify entries
	if count := store.Count(); count != 7 {
		t.Errorf("Count = %d, want 7", count)
	}

	// 6. Verify chain integrity
	result, err := store.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !result.Valid {
		t.Errorf("Chain should be valid: %s", result.Error)
	}

	// 7. Close and reopen store
	store.Close()

	store2, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("Reopen store: %v", err)
	}
	defer store2.Close()

	// 8. Verify chain still valid after reopen
	result2, err := store2.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain after reopen: %v", err)
	}
	if !result2.Valid {
		t.Errorf("Chain should still be valid after reopen: %s", result2.Error)
	}

	// 9. Add more entries after reopen
	store2.AppendConsole("after reopen")

	if count := store2.Count(); count != 8 {
		t.Errorf("Count after reopen = %d, want 8", count)
	}

	// 10. Final verification
	result3, _ := store2.VerifyChain()
	if !result3.Valid {
		t.Errorf("Chain should be valid after adding: %s", result3.Error)
	}
}
```

**Step 2: Run test**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
go test ./internal/audit/... -v -run TestIntegration
```

Expected: PASS

**Step 3: Commit**

```bash
git add internal/audit/integration_test.go
git commit -m "test(audit): add integration test for full workflow"
```

---

## Task 10: Final - Run All Tests and Document

**Step 1: Run all tests**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
go test ./internal/audit/... -v -cover
```

Expected: All tests pass with good coverage.

**Step 2: Run linter**

```bash
cd /Users/andybons/dev/moat/.worktrees/tamper-proof-logs
golangci-lint run ./internal/audit/...
```

Fix any issues found.

**Step 3: Commit any fixes**

```bash
git add .
git commit -m "chore(audit): address linter feedback"
```

---

## Summary

Phase 1 delivers:

| Component | Status |
|-----------|--------|
| `Entry` type with SHA-256 hashing | ✓ |
| `Store` with SQLite backend | ✓ |
| Hash chain append with prev_hash linking | ✓ |
| Query methods (Get, Count, Range) | ✓ |
| Chain verification with tamper detection | ✓ |
| Collector with Unix socket transport | ✓ |
| Collector with TCP + token auth transport | ✓ |
| Typed entry helpers (Console, Network, Credential) | ✓ |
| Integration tests | ✓ |

**Next Phase:** Merkle tree implementation with proof generation.
