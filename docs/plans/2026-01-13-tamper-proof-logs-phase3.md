# Tamper-Proof Logs Phase 3: Local Signing and Verification CLI

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add local signing of Merkle roots and a CLI command to audit and verify run integrity.

**Architecture:** Each run generates an Ed25519 keypair. Merkle roots are signed at checkpoints (critical events, batches). The `agent audit` command verifies the hash chain, Merkle tree, and all local signatures.

**Tech Stack:** Go, Ed25519 (`crypto/ed25519`), Cobra CLI, existing `internal/audit` package

---

## Task 1: Create Signer Type with Key Generation

**Files:**
- Create: `internal/audit/signer.go`
- Create: `internal/audit/signer_test.go`

**Step 1: Write the failing test**

Create `internal/audit/signer_test.go`:

```go
package audit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSigner_GenerateKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "run.key")

	signer, err := NewSigner(keyPath)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}

	// Key file should exist
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		t.Error("Key file should be created")
	}

	// Public key should be available
	if len(signer.PublicKey()) == 0 {
		t.Error("PublicKey should not be empty")
	}
}

func TestSigner_LoadExistingKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "run.key")

	// Create first signer
	signer1, _ := NewSigner(keyPath)
	pubKey1 := signer1.PublicKey()

	// Create second signer with same path - should load existing key
	signer2, _ := NewSigner(keyPath)
	pubKey2 := signer2.PublicKey()

	if string(pubKey1) != string(pubKey2) {
		t.Error("Loading existing key should return same public key")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestSigner -v
```

Expected: FAIL - NewSigner undefined

**Step 3: Write minimal implementation**

Create `internal/audit/signer.go`:

```go
package audit

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
)

// Signer handles Ed25519 signing for audit logs.
type Signer struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
}

// NewSigner creates or loads an Ed25519 keypair.
// If keyPath exists, loads the existing key. Otherwise generates a new one.
func NewSigner(keyPath string) (*Signer, error) {
	// Try to load existing key
	if data, err := os.ReadFile(keyPath); err == nil {
		block, _ := pem.Decode(data)
		if block == nil || block.Type != "PRIVATE KEY" {
			return nil, fmt.Errorf("invalid key file format")
		}
		privateKey := ed25519.PrivateKey(block.Bytes)
		return &Signer{
			privateKey: privateKey,
			publicKey:  privateKey.Public().(ed25519.PublicKey),
		}, nil
	}

	// Generate new keypair
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating key: %w", err)
	}

	// Save private key
	block := &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateKey,
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0600); err != nil {
		return nil, fmt.Errorf("saving key: %w", err)
	}

	return &Signer{
		privateKey: privateKey,
		publicKey:  publicKey,
	}, nil
}

// PublicKey returns the public key bytes.
func (s *Signer) PublicKey() []byte {
	return s.publicKey
}
```

**Step 4: Run test to verify it passes**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestSigner -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/audit/signer.go internal/audit/signer_test.go
git commit -m "feat(audit): add Signer type with Ed25519 key generation"
```

---

## Task 2: Add Sign and Verify Methods

**Files:**
- Modify: `internal/audit/signer.go`
- Modify: `internal/audit/signer_test.go`

**Step 1: Write the failing tests**

Add to `internal/audit/signer_test.go`:

```go
func TestSigner_SignAndVerify(t *testing.T) {
	dir := t.TempDir()
	signer, _ := NewSigner(filepath.Join(dir, "run.key"))

	message := []byte("merkle root hash abc123")
	signature := signer.Sign(message)

	if len(signature) == 0 {
		t.Fatal("Signature should not be empty")
	}

	if !signer.Verify(message, signature) {
		t.Error("Signature should verify")
	}
}

func TestSigner_VerifyTampered(t *testing.T) {
	dir := t.TempDir()
	signer, _ := NewSigner(filepath.Join(dir, "run.key"))

	message := []byte("merkle root hash abc123")
	signature := signer.Sign(message)

	// Tamper with message
	tampered := []byte("merkle root hash TAMPERED")

	if signer.Verify(tampered, signature) {
		t.Error("Tampered message should not verify")
	}
}

func TestSigner_VerifyWithPublicKeyOnly(t *testing.T) {
	dir := t.TempDir()
	signer, _ := NewSigner(filepath.Join(dir, "run.key"))

	message := []byte("merkle root hash abc123")
	signature := signer.Sign(message)

	// Verify with only public key (simulates third-party verification)
	valid := VerifySignature(signer.PublicKey(), message, signature)
	if !valid {
		t.Error("Should verify with public key only")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestSigner_Sign -v
```

Expected: FAIL - Sign method undefined

**Step 3: Write minimal implementation**

Add to `internal/audit/signer.go`:

```go
// Sign signs a message and returns the signature.
func (s *Signer) Sign(message []byte) []byte {
	return ed25519.Sign(s.privateKey, message)
}

// Verify checks if a signature is valid for the message.
func (s *Signer) Verify(message, signature []byte) bool {
	return ed25519.Verify(s.publicKey, message, signature)
}

// VerifySignature verifies a signature using only the public key.
// This is useful for third-party verification without the private key.
func VerifySignature(publicKey, message, signature []byte) bool {
	if len(publicKey) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(publicKey, message, signature)
}
```

**Step 4: Run test to verify it passes**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestSigner -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/audit/signer.go internal/audit/signer_test.go
git commit -m "feat(audit): add Sign and Verify methods to Signer"
```

---

## Task 3: Create Attestation Type and Storage

**Files:**
- Create: `internal/audit/attestation.go`
- Create: `internal/audit/attestation_test.go`

**Step 1: Write the failing test**

Create `internal/audit/attestation_test.go`:

```go
package audit

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAttestation_CreateAndSave(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, _ := OpenStore(dbPath)
	defer store.Close()

	// Add some entries
	store.AppendConsole("test log")
	root := store.MerkleRoot()

	// Create attestation
	signer, _ := NewSigner(filepath.Join(dir, "run.key"))
	att := &Attestation{
		Sequence:  1,
		RootHash:  root,
		Timestamp: time.Now().UTC(),
		PublicKey: signer.PublicKey(),
	}
	att.Signature = signer.Sign([]byte(att.RootHash))

	// Save to store
	err := store.SaveAttestation(att)
	if err != nil {
		t.Fatalf("SaveAttestation: %v", err)
	}
}

func TestAttestation_LoadAll(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, _ := OpenStore(dbPath)
	defer store.Close()

	// Add entries and attestations
	store.AppendConsole("test 1")
	signer, _ := NewSigner(filepath.Join(dir, "run.key"))

	att1 := &Attestation{
		Sequence:  1,
		RootHash:  store.MerkleRoot(),
		Timestamp: time.Now().UTC(),
		PublicKey: signer.PublicKey(),
	}
	att1.Signature = signer.Sign([]byte(att1.RootHash))
	store.SaveAttestation(att1)

	store.AppendConsole("test 2")
	att2 := &Attestation{
		Sequence:  2,
		RootHash:  store.MerkleRoot(),
		Timestamp: time.Now().UTC(),
		PublicKey: signer.PublicKey(),
	}
	att2.Signature = signer.Sign([]byte(att2.RootHash))
	store.SaveAttestation(att2)

	// Load all attestations
	attestations, err := store.LoadAttestations()
	if err != nil {
		t.Fatalf("LoadAttestations: %v", err)
	}

	if len(attestations) != 2 {
		t.Errorf("got %d attestations, want 2", len(attestations))
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestAttestation -v
```

Expected: FAIL - Attestation type undefined

**Step 3: Write minimal implementation**

Create `internal/audit/attestation.go`:

```go
package audit

import (
	"time"
)

// Attestation represents a signed checkpoint of the Merkle root.
type Attestation struct {
	Sequence  uint64    `json:"seq"`        // Entry sequence at checkpoint
	RootHash  string    `json:"root_hash"`  // Merkle root at this point
	Timestamp time.Time `json:"timestamp"`  // When attestation was created
	Signature []byte    `json:"signature"`  // Ed25519 signature of root hash
	PublicKey []byte    `json:"public_key"` // Signer's public key
}

// Verify checks if the attestation signature is valid.
func (a *Attestation) Verify() bool {
	return VerifySignature(a.PublicKey, []byte(a.RootHash), a.Signature)
}
```

Add to `internal/audit/store.go` - update createTables:

```go
// Add attestations table to createTables
CREATE TABLE IF NOT EXISTS attestations (
	seq       INTEGER PRIMARY KEY,
	root_hash TEXT NOT NULL,
	timestamp TEXT NOT NULL,
	signature BLOB NOT NULL,
	public_key BLOB NOT NULL
);
```

Add to `internal/audit/store.go`:

```go
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
```

**Step 4: Run test to verify it passes**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestAttestation -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/audit/attestation.go internal/audit/attestation_test.go internal/audit/store.go
git commit -m "feat(audit): add Attestation type with storage"
```

---

## Task 4: Create Auditor Type for Verification

**Files:**
- Create: `internal/audit/auditor.go`
- Create: `internal/audit/auditor_test.go`

**Step 1: Write the failing test**

Create `internal/audit/auditor_test.go`:

```go
package audit

import (
	"path/filepath"
	"testing"
)

func TestAuditor_VerifyAll_Valid(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, _ := OpenStore(dbPath)

	// Add entries
	for i := 0; i < 10; i++ {
		store.AppendConsole("log line")
	}

	// Add attestation
	signer, _ := NewSigner(filepath.Join(dir, "run.key"))
	att := &Attestation{
		Sequence:  10,
		RootHash:  store.MerkleRoot(),
		Timestamp: time.Now().UTC(),
		PublicKey: signer.PublicKey(),
	}
	att.Signature = signer.Sign([]byte(att.RootHash))
	store.SaveAttestation(att)
	store.Close()

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
}

func TestAuditor_VerifyAll_TamperedEntry(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, _ := OpenStore(dbPath)

	for i := 0; i < 5; i++ {
		store.AppendConsole("log line")
	}
	store.Close()

	// Tamper with an entry directly in the database
	db, _ := sql.Open("sqlite", dbPath)
	db.Exec(`UPDATE entries SET data = '{"line":"TAMPERED"}' WHERE seq = 3`)
	db.Close()

	// Audit should detect tampering
	auditor, _ := NewAuditor(dbPath)
	defer auditor.Close()

	result, _ := auditor.Verify()

	if result.Valid {
		t.Error("Should detect tampered entry")
	}
	if result.HashChainValid {
		t.Error("Hash chain should be invalid")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestAuditor -v
```

Expected: FAIL - NewAuditor undefined

**Step 3: Write minimal implementation**

Create `internal/audit/auditor.go`:

```go
package audit

import "fmt"

// AuditResult contains the results of verifying a run's integrity.
type AuditResult struct {
	Valid             bool   `json:"valid"`
	HashChainValid    bool   `json:"hash_chain_valid"`
	MerkleRootValid   bool   `json:"merkle_root_valid"`
	AttestationsValid bool   `json:"attestations_valid"`
	EntryCount        uint64 `json:"entry_count"`
	AttestationCount  int    `json:"attestation_count"`
	Error             string `json:"error,omitempty"`
}

// Auditor verifies the integrity of a run's audit logs.
type Auditor struct {
	store *Store
}

// NewAuditor creates an auditor for the given database path.
func NewAuditor(dbPath string) (*Auditor, error) {
	store, err := OpenStore(dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening store: %w", err)
	}
	return &Auditor{store: store}, nil
}

// Close closes the auditor's store.
func (a *Auditor) Close() error {
	return a.store.Close()
}

// Verify performs a full integrity verification.
func (a *Auditor) Verify() (*AuditResult, error) {
	result := &AuditResult{
		Valid:             true,
		HashChainValid:    true,
		MerkleRootValid:   true,
		AttestationsValid: true,
	}

	// Verify hash chain
	chainResult, err := a.store.VerifyChain()
	if err != nil {
		return nil, fmt.Errorf("verifying chain: %w", err)
	}
	result.EntryCount = chainResult.EntryCount
	if !chainResult.Valid {
		result.Valid = false
		result.HashChainValid = false
		result.Error = chainResult.Error
		return result, nil
	}

	// Verify Merkle root
	entries, err := a.store.Range(1, chainResult.EntryCount)
	if err != nil {
		return nil, fmt.Errorf("loading entries: %w", err)
	}
	tree := BuildMerkleTree(entries)
	storedRoot := a.store.MerkleRoot()
	if tree.RootHash() != storedRoot {
		result.Valid = false
		result.MerkleRootValid = false
		result.Error = "merkle root mismatch: stored root doesn't match computed root"
		return result, nil
	}

	// Verify attestations
	attestations, err := a.store.LoadAttestations()
	if err != nil {
		return nil, fmt.Errorf("loading attestations: %w", err)
	}
	result.AttestationCount = len(attestations)

	for _, att := range attestations {
		if !att.Verify() {
			result.Valid = false
			result.AttestationsValid = false
			result.Error = fmt.Sprintf("invalid signature on attestation at seq %d", att.Sequence)
			return result, nil
		}
	}

	return result, nil
}
```

**Step 4: Run test to verify it passes**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestAuditor -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/audit/auditor.go internal/audit/auditor_test.go
git commit -m "feat(audit): add Auditor type for verification"
```

---

## Task 5: Add CLI Audit Command

**Files:**
- Create: `cmd/agent/audit.go`
- Modify: `cmd/agent/root.go` (add audit command)

**Step 1: Write the implementation**

Create `cmd/agent/audit.go`:

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/andybons/agentops/internal/audit"
	"github.com/spf13/cobra"
)

var auditCmd = &cobra.Command{
	Use:   "audit <run-id>",
	Short: "Verify the integrity of a run's audit logs",
	Long: `Verify the cryptographic integrity of a run's audit logs.

Checks:
  - Hash chain: All entries are properly linked
  - Merkle tree: Root matches computed root from entries
  - Signatures: All attestations have valid signatures

Example:
  agent audit run-abc123def456`,
	Args: cobra.ExactArgs(1),
	RunE: runAudit,
}

func init() {
	rootCmd.AddCommand(auditCmd)
}

func runAudit(cmd *cobra.Command, args []string) error {
	runID := args[0]

	// Find run directory
	homeDir, _ := os.UserHomeDir()
	runDir := filepath.Join(homeDir, ".agentops", "runs", runID)
	dbPath := filepath.Join(runDir, "logs.db")

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("run not found: %s", runID)
	}

	fmt.Printf("Auditing run: %s\n", runID)
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	auditor, err := audit.NewAuditor(dbPath)
	if err != nil {
		return fmt.Errorf("opening audit log: %w", err)
	}
	defer auditor.Close()

	result, err := auditor.Verify()
	if err != nil {
		return fmt.Errorf("verification error: %w", err)
	}

	// Print results
	fmt.Println("Log Integrity")
	if result.HashChainValid {
		fmt.Printf("  ✓ Hash chain: %d entries, no gaps, all hashes valid\n", result.EntryCount)
	} else {
		fmt.Printf("  ✗ Hash chain: INVALID\n")
	}

	if result.MerkleRootValid {
		fmt.Println("  ✓ Merkle tree: root matches computed root")
	} else {
		fmt.Println("  ✗ Merkle tree: INVALID")
	}

	fmt.Println()
	fmt.Println("Local Signatures")
	if result.AttestationCount == 0 {
		fmt.Println("  - No attestations found")
	} else if result.AttestationsValid {
		fmt.Printf("  ✓ %d attestations, all signatures valid\n", result.AttestationCount)
	} else {
		fmt.Printf("  ✗ Attestations: INVALID\n")
	}

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════════")

	if result.Valid {
		fmt.Println("VERDICT: ✓ INTACT - No tampering detected")
		return nil
	}

	fmt.Printf("VERDICT: ✗ TAMPERED - %s\n", result.Error)
	os.Exit(1)
	return nil
}
```

**Step 2: Test manually**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go build ./cmd/agent
./agent audit test-run  # Should show "run not found" for non-existent run
```

**Step 3: Commit**

```bash
git add cmd/agent/audit.go
git commit -m "feat(cli): add agent audit command"
```

---

## Task 6: Integration Test - Full Audit Workflow

**Files:**
- Modify: `internal/audit/integration_test.go`

**Step 1: Write the integration test**

Add to `internal/audit/integration_test.go`:

```go
func TestIntegration_AuditWorkflow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "logs.db")

	// Create store and add entries
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	// Simulate a run with various entry types
	for i := 0; i < 20; i++ {
		store.AppendConsole(fmt.Sprintf("log line %d", i))
	}
	store.AppendNetwork(NetworkData{
		Method:     "GET",
		URL:        "https://api.github.com/user",
		StatusCode: 200,
		DurationMs: 150,
	})
	store.AppendCredential(CredentialData{
		Name:   "github",
		Action: "injected",
		Host:   "api.github.com",
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
```

**Step 2: Run test**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestIntegration_AuditWorkflow -v
```

Expected: PASS

**Step 3: Commit**

```bash
git add internal/audit/integration_test.go
git commit -m "test(audit): add audit workflow integration test"
```

---

## Task 7: Final - Run All Tests and Lint

**Step 1: Run all tests with coverage**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -v -cover
```

Expected: All tests pass with good coverage (>80%).

**Step 2: Run linter**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
golangci-lint run ./internal/audit/...
```

Fix any issues found.

**Step 3: Commit any fixes**

```bash
git add .
git commit -m "chore(audit): address linter feedback for Phase 3"
```

---

## Summary

Phase 3 delivers:

| Component | Description |
|-----------|-------------|
| `Signer` | Ed25519 key generation, signing, verification |
| `Attestation` | Signed checkpoint with storage |
| `Auditor` | Full verification of chain, tree, signatures |
| `agent audit` | CLI command for run verification |

Next phase: Sigstore/Rekor integration for external attestation.
