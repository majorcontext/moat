package audit

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestAuditor_VerifyAll_Valid(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	// Add entries
	for i := 0; i < 10; i++ {
		if _, err := store.AppendConsole("log line"); err != nil {
			t.Fatalf("AppendConsole: %v", err)
		}
	}

	// Add attestation
	signer, err := NewSigner(filepath.Join(dir, "run.key"))
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	att := &Attestation{
		Sequence:  10,
		RootHash:  store.MerkleRoot(),
		Timestamp: time.Now().UTC(),
		PublicKey: signer.PublicKey(),
	}
	att.Signature = signer.Sign([]byte(att.RootHash))
	if err := store.SaveAttestation(att); err != nil {
		t.Fatalf("SaveAttestation: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// Audit
	auditor, err := NewAuditor(dbPath)
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}
	defer auditor.Close()

	result, err := auditor.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if !result.Valid {
		t.Errorf("Expected valid, got error: %s", result.Error)
	}
	if !result.HashChainValid {
		t.Error("Hash chain should be valid")
	}
	if !result.MerkleRootValid {
		t.Error("Merkle root should be valid")
	}
	if !result.AttestationsValid {
		t.Error("Attestations should be valid")
	}
	if result.EntryCount != 10 {
		t.Errorf("Expected 10 entries, got %d", result.EntryCount)
	}
	if result.AttestationCount != 1 {
		t.Errorf("Expected 1 attestation, got %d", result.AttestationCount)
	}
}

func TestAuditor_VerifyAll_TamperedEntry(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	for i := 0; i < 5; i++ {
		if _, err := store.AppendConsole("log line"); err != nil {
			t.Fatalf("AppendConsole: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// Tamper with an entry directly in the database
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`UPDATE entries SET data = '{"line":"TAMPERED"}' WHERE seq = 3`); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	// Audit should detect tampering
	auditor, err := NewAuditor(dbPath)
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}
	defer auditor.Close()

	result, err := auditor.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if result.Valid {
		t.Error("Should detect tampered entry")
	}
	if result.HashChainValid {
		t.Error("Hash chain should be invalid")
	}
}

func TestAuditor_VerifyAll_TamperedMerkleRoot(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	for i := 0; i < 5; i++ {
		if _, err := store.AppendConsole("log line"); err != nil {
			t.Fatalf("AppendConsole: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// Tamper with the merkle root directly in the database
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`UPDATE metadata SET value = 'tampered_root_hash' WHERE key = 'merkle_root'`); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	// Audit should detect tampering
	auditor, err := NewAuditor(dbPath)
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}
	defer auditor.Close()

	result, err := auditor.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if result.Valid {
		t.Error("Should detect tampered merkle root")
	}
	if !result.HashChainValid {
		t.Error("Hash chain should still be valid (tampering was on merkle root)")
	}
	if result.MerkleRootValid {
		t.Error("Merkle root should be invalid")
	}
}

func TestAuditor_VerifyAll_InvalidAttestation(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	for i := 0; i < 5; i++ {
		if _, err := store.AppendConsole("log line"); err != nil {
			t.Fatalf("AppendConsole: %v", err)
		}
	}

	// Add attestation with invalid signature
	signer, err := NewSigner(filepath.Join(dir, "run.key"))
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	att := &Attestation{
		Sequence:  5,
		RootHash:  store.MerkleRoot(),
		Timestamp: time.Now().UTC(),
		PublicKey: signer.PublicKey(),
		Signature: []byte("invalid signature bytes that won't verify"),
	}
	if err := store.SaveAttestation(att); err != nil {
		t.Fatalf("SaveAttestation: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// Audit should detect invalid attestation
	auditor, err := NewAuditor(dbPath)
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}
	defer auditor.Close()

	result, err := auditor.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if result.Valid {
		t.Error("Should detect invalid attestation")
	}
	if !result.HashChainValid {
		t.Error("Hash chain should be valid")
	}
	if !result.MerkleRootValid {
		t.Error("Merkle root should be valid")
	}
	if result.AttestationsValid {
		t.Error("Attestations should be invalid")
	}
}

func TestAuditor_VerifyAll_EmptyStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// Audit empty store should be valid
	auditor, err := NewAuditor(dbPath)
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}
	defer auditor.Close()

	result, err := auditor.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if !result.Valid {
		t.Errorf("Empty store should be valid, got error: %s", result.Error)
	}
	if result.EntryCount != 0 {
		t.Errorf("Expected 0 entries, got %d", result.EntryCount)
	}
	if result.AttestationCount != 0 {
		t.Errorf("Expected 0 attestations, got %d", result.AttestationCount)
	}
}

func TestAuditor_VerifyAll_BrokenChain(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	for i := 0; i < 5; i++ {
		if _, err := store.AppendConsole("log line"); err != nil {
			t.Fatalf("AppendConsole: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// Break the chain by modifying prev_hash
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`UPDATE entries SET prev_hash = 'broken_hash' WHERE seq = 3`); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	// Audit should detect broken chain
	auditor, err := NewAuditor(dbPath)
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}
	defer auditor.Close()

	result, err := auditor.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if result.Valid {
		t.Error("Should detect broken chain")
	}
	if result.HashChainValid {
		t.Error("Hash chain should be invalid")
	}
}

func TestAuditor_VerifyAll_WithRekorProofs(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	// Add entries
	for i := 0; i < 5; i++ {
		if _, err := store.AppendConsole("log line"); err != nil {
			t.Fatalf("AppendConsole: %v", err)
		}
	}

	// Add Rekor proof
	proof := &RekorProof{
		LogIndex:  12345,
		LogID:     "test-log-id",
		TreeSize:  98765,
		RootHash:  store.MerkleRoot(),
		Timestamp: time.Now().UTC(),
		EntryUUID: "test-uuid",
	}
	if err := store.SaveRekorProof(5, proof); err != nil {
		t.Fatalf("SaveRekorProof: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// Audit
	auditor, err := NewAuditor(dbPath)
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}
	defer auditor.Close()

	result, err := auditor.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if !result.Valid {
		t.Errorf("Expected valid, got error: %s", result.Error)
	}
	if result.RekorProofCount != 1 {
		t.Errorf("RekorProofCount = %d, want 1", result.RekorProofCount)
	}
}
