package audit

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIntegration_FullWorkflow(t *testing.T) {
	// Create a short socket path to avoid Unix socket path length limits.
	// Unix domain sockets have a max path length of ~104 bytes on macOS.
	socketDir, err := os.MkdirTemp("", "sock")
	if err != nil {
		t.Fatalf("creating socket temp dir: %v", err)
	}
	defer os.RemoveAll(socketDir)
	socketPath := filepath.Join(socketDir, "s")

	// Use t.TempDir() for the database (path length doesn't matter for SQLite)
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "logs.db")

	// 1. Create store
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	// 2. Start collector
	collector := NewCollector(store)
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
		if err := json.NewEncoder(conn).Encode(msg); err != nil {
			t.Fatalf("Encode console message %d: %v", i, err)
		}
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
	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		t.Fatalf("Encode network message: %v", err)
	}

	// Write credential event
	msg = CollectorMessage{
		Type: "credential",
		Data: map[string]any{
			"name":   "github",
			"action": "injected",
			"host":   "api.github.com",
		},
	}
	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		t.Fatalf("Encode credential message: %v", err)
	}

	conn.Close()
	time.Sleep(100 * time.Millisecond)

	// 4. Stop collector
	collector.Stop()

	// 5. Verify entries (use proper error handling for Count)
	count, err := store.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 7 {
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
	if _, err := store2.AppendConsole("after reopen"); err != nil {
		t.Fatalf("AppendConsole after reopen: %v", err)
	}

	count2, err := store2.Count()
	if err != nil {
		t.Fatalf("Count after reopen: %v", err)
	}
	if count2 != 8 {
		t.Errorf("Count after reopen = %d, want 8", count2)
	}

	// 10. Final verification
	result3, err := store2.VerifyChain()
	if err != nil {
		t.Fatalf("Final VerifyChain: %v", err)
	}
	if !result3.Valid {
		t.Errorf("Chain should be valid after adding: %s", result3.Error)
	}
}

func TestIntegration_MerkleProofs(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "logs.db")

	// Create store
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	// Add entries
	for i := 0; i < 10; i++ {
		_, err := store.AppendConsole(fmt.Sprintf("log line %d", i))
		if err != nil {
			t.Fatalf("AppendConsole: %v", err)
		}
	}

	// Verify merkle root exists
	root := store.MerkleRoot()
	if root == "" {
		t.Fatal("MerkleRoot should not be empty")
	}

	// Generate and verify proofs for all entries
	for seq := uint64(1); seq <= 10; seq++ {
		proof, err := store.ProveEntry(seq)
		if err != nil {
			t.Fatalf("ProveEntry(%d): %v", seq, err)
		}

		if !proof.Verify() {
			t.Errorf("Proof for entry %d should verify", seq)
		}

		if proof.RootHash != root {
			t.Errorf("Proof root should match store root")
		}
	}

	// Close and reopen - proofs should still work
	store.Close()

	store2, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer store2.Close()

	// Root should persist
	if store2.MerkleRoot() != root {
		t.Error("MerkleRoot should persist across reopen")
	}

	// Proofs should still verify
	proof, _ := store2.ProveEntry(5)
	if !proof.Verify() {
		t.Error("Proof should still verify after reopen")
	}

	// Add more entries and verify new proofs
	store2.AppendConsole("after reopen")
	newRoot := store2.MerkleRoot()
	if newRoot == root {
		t.Error("Root should change after new entry")
	}

	proof2, _ := store2.ProveEntry(11)
	if !proof2.Verify() {
		t.Error("New entry proof should verify")
	}
	if proof2.RootHash != newRoot {
		t.Error("New proof should use new root")
	}
}

func TestIntegration_RekorWorkflow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "logs.db")

	// Create store and add entries
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	// Simulate a run with network requests (high-value events)
	// Use maps to ensure consistent JSON serialization (matches how Collector works)
	for i := 0; i < 10; i++ {
		store.Append(EntryConsole, map[string]any{"line": fmt.Sprintf("log line %d", i)})
	}
	store.Append(EntryNetwork, map[string]any{
		"method":      "GET",
		"url":         "https://api.github.com/user",
		"status_code": 200,
		"duration_ms": 150,
	})

	// Create local attestation
	signer, _ := NewSigner(filepath.Join(dir, "run.key"))
	att := &Attestation{
		Sequence:  11,
		RootHash:  store.MerkleRoot(),
		Timestamp: time.Now().UTC(),
		PublicKey: signer.PublicKey(),
	}
	att.Signature = signer.Sign([]byte(att.RootHash))
	store.SaveAttestation(att)

	// Simulate Rekor proof (would come from actual Rekor submission)
	rekorProof := &RekorProof{
		LogIndex:  12345678,
		LogID:     "c0d23d6ad406973f9ef8b320e5e4e4692e0e65e5419ad4e30c9a8b912a8a3b5c",
		TreeSize:  98765432,
		RootHash:  store.MerkleRoot(),
		Hashes:    []string{"hash1", "hash2"},
		Timestamp: time.Now().UTC(),
		EntryUUID: "24296fb24b8ad77a8c6e7c4b2e5ac5d8e9f0a1b2c3d4e5f6",
	}
	store.SaveRekorProof(11, rekorProof)

	store.Close()

	// Audit the run
	auditor, err := NewAuditor(dbPath)
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}
	defer auditor.Close()

	result, err := auditor.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// All checks should pass
	if !result.Valid {
		t.Errorf("Expected valid audit, got error: %s", result.Error)
	}
	if result.EntryCount != 11 {
		t.Errorf("EntryCount = %d, want 11", result.EntryCount)
	}
	if result.AttestationCount != 1 {
		t.Errorf("AttestationCount = %d, want 1", result.AttestationCount)
	}
	if result.RekorProofCount != 1 {
		t.Errorf("RekorProofCount = %d, want 1", result.RekorProofCount)
	}
}

func TestIntegration_AuditWorkflow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "logs.db")

	// Create store and add entries
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	// Simulate a run with various entry types
	// Use maps to ensure consistent JSON serialization (matches how Collector works)
	for i := 0; i < 20; i++ {
		store.Append(EntryConsole, map[string]any{"line": fmt.Sprintf("log line %d", i)})
	}
	store.Append(EntryNetwork, map[string]any{
		"method":      "GET",
		"url":         "https://api.github.com/user",
		"status_code": 200,
		"duration_ms": 150,
	})
	store.Append(EntryCredential, map[string]any{
		"name":   "github",
		"action": "injected",
		"host":   "api.github.com",
	})

	// Create attestation at checkpoint
	signer, _ := NewSigner(filepath.Join(dir, "run.key"))
	att := &Attestation{
		Sequence:  22,
		RootHash:  store.MerkleRoot(),
		Timestamp: time.Now().UTC(),
		PublicKey: signer.PublicKey(),
	}
	att.Signature = signer.Sign([]byte(att.RootHash))
	store.SaveAttestation(att)

	store.Close()

	// Audit the run
	auditor, err := NewAuditor(dbPath)
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}
	defer auditor.Close()

	result, err := auditor.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// All checks should pass
	if !result.Valid {
		t.Errorf("Expected valid audit, got error: %s", result.Error)
	}
	if result.EntryCount != 22 {
		t.Errorf("EntryCount = %d, want 22", result.EntryCount)
	}
	if result.AttestationCount != 1 {
		t.Errorf("AttestationCount = %d, want 1", result.AttestationCount)
	}
}
