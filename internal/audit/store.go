package audit

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite" // SQLite driver registration
)

// ErrNotFound is returned when an entry doesn't exist.
var ErrNotFound = errors.New("entry not found")

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
