package audit

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/andybons/moat/internal/log"
	_ "modernc.org/sqlite" // SQLite driver registration
)

// ErrNotFound is returned when an entry doesn't exist.
var ErrNotFound = errors.New("entry not found")

// Store provides tamper-proof log storage using SQLite.
type Store struct {
	db         *sql.DB
	mu         sync.Mutex
	lastHash   string
	lastSeq    uint64
	merkleRoot string                 // Current Merkle tree root hash
	merkleTree *IncrementalMerkleTree // In-memory tree for O(log n) appends
}

// OpenStore opens or creates a log store at the given path.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Enable WAL mode for better concurrent read/write performance.
	// WAL allows readers to not block writers and vice versa.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
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

		CREATE TABLE IF NOT EXISTS metadata (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS attestations (
			seq        INTEGER PRIMARY KEY,
			root_hash  TEXT NOT NULL,
			timestamp  TEXT NOT NULL,
			signature  BLOB NOT NULL,
			public_key BLOB NOT NULL
		);

		CREATE TABLE IF NOT EXISTS rekor_proofs (
			seq        INTEGER PRIMARY KEY,
			log_index  INTEGER NOT NULL,
			log_id     TEXT NOT NULL,
			tree_size  INTEGER NOT NULL,
			root_hash  TEXT NOT NULL,
			hashes     TEXT NOT NULL,
			timestamp  TEXT NOT NULL,
			entry_uuid TEXT NOT NULL
		);
	`)
	return err
}

func (s *Store) loadLastEntry() error {
	// Initialize incremental tree
	s.merkleTree = NewIncrementalMerkleTree()

	// Load last entry state
	row := s.db.QueryRow(`
		SELECT seq, hash FROM entries ORDER BY seq DESC LIMIT 1
	`)
	var seq uint64
	var hash string
	err := row.Scan(&seq, &hash)
	switch {
	case err == sql.ErrNoRows:
		// Empty store - no entries yet
		return nil
	case err != nil:
		return fmt.Errorf("loading last entry: %w", err)
	default:
		s.lastSeq = seq
		s.lastHash = hash
	}

	// Load stored merkle root from database (may be tampered - auditor will verify)
	row = s.db.QueryRow(`SELECT value FROM metadata WHERE key = 'merkle_root'`)
	var root string
	err = row.Scan(&root)
	switch {
	case err == sql.ErrNoRows:
		// No root yet
	case err != nil:
		return fmt.Errorf("loading merkle root: %w", err)
	default:
		s.merkleRoot = root
	}

	// Rebuild incremental tree from all entries (one-time O(n) on open)
	// This is used for future O(log n) appends, not for verification
	rows, err := s.db.Query(`SELECT seq, hash FROM entries ORDER BY seq`)
	if err != nil {
		return fmt.Errorf("loading entries for merkle tree: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var entrySeq uint64
		var entryHash string
		if err := rows.Scan(&entrySeq, &entryHash); err != nil {
			return fmt.Errorf("scanning entry for merkle tree: %w", err)
		}
		s.merkleTree.Append(entrySeq, entryHash)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating entries for merkle tree: %w", err)
	}

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

	// Update merkle tree incrementally - O(log n) instead of O(n)
	s.merkleTree.Append(entry.Sequence, entry.Hash)
	s.merkleRoot = s.merkleTree.RootHash()

	// Persist merkle root to metadata.
	// If this fails, the auditor will detect a mismatch between stored and
	// computed roots during verification. We log the error but don't fail
	// the append - the entry is already committed and the in-memory tree
	// is correct. Verification will catch any persistence issues.
	if _, err := s.db.Exec(`
		INSERT INTO metadata (key, value) VALUES ('merkle_root', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, s.merkleRoot); err != nil {
		log.Warn("failed to persist merkle root", "error", err)
	}

	return entry, nil
}

// MerkleRoot returns the current Merkle tree root hash.
func (s *Store) MerkleRoot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.merkleRoot
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

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

// AppendSecret adds a secret resolution entry.
func (s *Store) AppendSecret(data SecretData) (*Entry, error) {
	return s.Append(EntrySecret, &data)
}

// AppendSSH adds an SSH agent operation entry.
func (s *Store) AppendSSH(data SSHData) (*Entry, error) {
	return s.Append(EntrySSH, &data)
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
func (s *Store) Count() (uint64, error) {
	var count uint64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM entries`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting entries: %w", err)
	}
	return count, nil
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

	// Parse errors are safe to ignore: Append() writes RFC3339Nano format,
	// so any value read from the database will parse successfully.
	e.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
	// Store the original JSON for hash verification (avoids map key ordering issues)
	e.dataJSON = []byte(dataStr)
	// Unmarshal errors are safe to ignore: Append() validates JSON marshaling
	// before writing, so any value read from the database is valid JSON.
	_ = json.Unmarshal([]byte(dataStr), &e.Data) //nolint:errcheck // documented safe above
	return &e, nil
}

func scanEntryRows(rows *sql.Rows) (*Entry, error) {
	var e Entry
	var tsStr, dataStr string
	err := rows.Scan(&e.Sequence, &tsStr, &e.Type, &e.PrevHash, &dataStr, &e.Hash)
	if err != nil {
		return nil, fmt.Errorf("scanning entry: %w", err)
	}

	// Parse errors are safe to ignore: Append() writes RFC3339Nano format,
	// so any value read from the database will parse successfully.
	e.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
	// Store the original JSON for hash verification (avoids map key ordering issues)
	e.dataJSON = []byte(dataStr)
	// Unmarshal errors are safe to ignore: Append() validates JSON marshaling
	// before writing, so any value read from the database is valid JSON.
	_ = json.Unmarshal([]byte(dataStr), &e.Data) //nolint:errcheck // documented safe above
	return &e, nil
}

// VerifyResult contains the result of chain verification.
type VerifyResult struct {
	Valid           bool
	EntryCount      uint64
	FirstInvalidSeq uint64
	Error           string
}

// ProveEntry generates an inclusion proof for the given sequence number.
func (s *Store) ProveEntry(seq uint64) (*InclusionProof, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Rebuild tree from entries
	entries, err := s.Range(1, s.lastSeq)
	if err != nil {
		return nil, fmt.Errorf("loading entries: %w", err)
	}

	tree := BuildMerkleTree(entries)
	return tree.ProveInclusion(seq)
}

// SaveAttestation saves an attestation to the store.
func (s *Store) SaveAttestation(att *Attestation) error {
	_, err := s.db.Exec(`
		INSERT INTO attestations (seq, root_hash, timestamp, signature, public_key)
		VALUES (?, ?, ?, ?, ?)
	`, att.Sequence, att.RootHash, att.Timestamp.Format(time.RFC3339Nano),
		att.Signature, att.PublicKey)
	if err != nil {
		return fmt.Errorf("saving attestation: %w", err)
	}
	return nil
}

// LoadAttestations returns all attestations in the store.
func (s *Store) LoadAttestations() ([]*Attestation, error) {
	rows, err := s.db.Query(`
		SELECT seq, root_hash, timestamp, signature, public_key
		FROM attestations ORDER BY seq
	`)
	if err != nil {
		return nil, fmt.Errorf("loading attestations: %w", err)
	}
	defer rows.Close()

	var attestations []*Attestation
	for rows.Next() {
		var att Attestation
		var tsStr string
		if err := rows.Scan(&att.Sequence, &att.RootHash, &tsStr, &att.Signature, &att.PublicKey); err != nil {
			return nil, fmt.Errorf("scanning attestation: %w", err)
		}
		att.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
		attestations = append(attestations, &att)
	}
	return attestations, rows.Err()
}

// SaveRekorProof saves a Rekor inclusion proof for an entry.
func (s *Store) SaveRekorProof(seq uint64, proof *RekorProof) error {
	hashesJSON, _ := json.Marshal(proof.Hashes)
	_, err := s.db.Exec(`
		INSERT INTO rekor_proofs (seq, log_index, log_id, tree_size, root_hash, hashes, timestamp, entry_uuid)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, seq, proof.LogIndex, proof.LogID, proof.TreeSize, proof.RootHash,
		string(hashesJSON), proof.Timestamp.Format(time.RFC3339Nano), proof.EntryUUID)
	if err != nil {
		return fmt.Errorf("saving rekor proof: %w", err)
	}
	return nil
}

// LoadRekorProofs returns all Rekor proofs in the store.
func (s *Store) LoadRekorProofs() (map[uint64]*RekorProof, error) {
	rows, err := s.db.Query(`
		SELECT seq, log_index, log_id, tree_size, root_hash, hashes, timestamp, entry_uuid
		FROM rekor_proofs ORDER BY seq
	`)
	if err != nil {
		return nil, fmt.Errorf("loading rekor proofs: %w", err)
	}
	defer rows.Close()

	proofs := make(map[uint64]*RekorProof)
	for rows.Next() {
		var seq uint64
		var proof RekorProof
		var hashesJSON, tsStr string
		if err := rows.Scan(&seq, &proof.LogIndex, &proof.LogID, &proof.TreeSize,
			&proof.RootHash, &hashesJSON, &tsStr, &proof.EntryUUID); err != nil {
			return nil, fmt.Errorf("scanning rekor proof: %w", err)
		}
		if err := json.Unmarshal([]byte(hashesJSON), &proof.Hashes); err != nil {
			return nil, fmt.Errorf("unmarshaling rekor hashes: %w", err)
		}
		proof.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
		proofs[seq] = &proof
	}
	return proofs, rows.Err()
}

// Export creates a portable proof bundle from the store.
func (s *Store) Export() (*ProofBundle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Load all entries
	entries, err := s.Range(1, s.lastSeq)
	if err != nil {
		return nil, fmt.Errorf("loading entries: %w", err)
	}

	// Load attestations
	attestations, err := s.LoadAttestations()
	if err != nil {
		return nil, fmt.Errorf("loading attestations: %w", err)
	}

	// Load Rekor proofs
	rekorProofsMap, err := s.LoadRekorProofs()
	if err != nil {
		return nil, fmt.Errorf("loading rekor proofs: %w", err)
	}

	// Convert map to slice
	rekorProofs := make([]*RekorProof, 0, len(rekorProofsMap))
	for _, p := range rekorProofsMap {
		rekorProofs = append(rekorProofs, p)
	}

	return &ProofBundle{
		Version:      BundleVersion,
		CreatedAt:    time.Now().UTC(),
		MerkleRoot:   s.merkleRoot,
		Entries:      entries,
		Attestations: attestations,
		RekorProofs:  rekorProofs,
	}, nil
}

// ExportWithProofs creates a proof bundle with inclusion proofs for specific entries.
// This is useful for proving specific entries without including the full log.
func (s *Store) ExportWithProofs(seqs []uint64) (*ProofBundle, error) {
	bundle, err := s.Export()
	if err != nil {
		return nil, err
	}

	// Generate inclusion proofs for requested entries
	for _, seq := range seqs {
		proof, err := s.ProveEntry(seq)
		if err != nil {
			return nil, fmt.Errorf("proving entry %d: %w", seq, err)
		}
		bundle.Proofs = append(bundle.Proofs, proof)
	}

	return bundle, nil
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
