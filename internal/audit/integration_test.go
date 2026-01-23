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
		RootHash:  store.LastHash(),
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
		RootHash:  store.LastHash(),
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
		RootHash:  store.LastHash(),
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

func TestIntegration_BundleWorkflow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "logs.db")

	// === PRODUCER: Create and populate store ===
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	// Add various entry types
	// Use maps to ensure consistent JSON serialization (matches how Collector works)
	for i := 0; i < 20; i++ {
		store.Append(EntryConsole, map[string]any{"line": fmt.Sprintf("log line %d", i)})
	}
	store.Append(EntryNetwork, map[string]any{
		"method":      "POST",
		"url":         "https://api.example.com/data",
		"status_code": 201,
		"duration_ms": 250,
	})
	store.Append(EntryCredential, map[string]any{
		"name":   "github",
		"action": "injected",
		"host":   "api.github.com",
	})

	// Create local attestation
	signer, _ := NewSigner(filepath.Join(dir, "run.key"))
	att := &Attestation{
		Sequence:  22,
		RootHash:  store.LastHash(),
		Timestamp: time.Now().UTC(),
		PublicKey: signer.PublicKey(),
	}
	att.Signature = signer.Sign([]byte(att.RootHash))
	store.SaveAttestation(att)

	// Add Rekor proof
	rekorProof := &RekorProof{
		LogIndex:  99999,
		LogID:     "test-log-id",
		TreeSize:  1000000,
		RootHash:  store.LastHash(),
		Timestamp: time.Now().UTC(),
		EntryUUID: "test-uuid",
	}
	store.SaveRekorProof(22, rekorProof)

	// Export bundle
	bundle, err := store.Export()
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	store.Close()

	// === TRANSPORT: Serialize and "send" ===
	bundleJSON, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	t.Logf("Bundle size: %d bytes", len(bundleJSON))

	// === CONSUMER: Receive and verify ===
	var received ProofBundle
	if err := json.Unmarshal(bundleJSON, &received); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Verify the bundle
	result := received.Verify()
	if !result.Valid {
		t.Errorf("Bundle verification failed: %s", result.Error)
	}

	// Check counts
	if result.EntryCount != 22 {
		t.Errorf("EntryCount = %d, want 22", result.EntryCount)
	}
	if result.AttestationCount != 1 {
		t.Errorf("AttestationCount = %d, want 1", result.AttestationCount)
	}
	if result.RekorProofCount != 1 {
		t.Errorf("RekorProofCount = %d, want 1", result.RekorProofCount)
	}
}
