# Tamper-Proof Logs Phase 5: Exportable Proof Bundles and Go API

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Create portable proof bundles that can verify audit logs without access to the original database, plus a clean Go API for programmatic access.

**Architecture:** A ProofBundle is a self-contained JSON structure with all entries, Merkle root, attestations, Rekor proofs, and optional inclusion proofs. The bundle can be exported from a store, shared, and verified offline. The Go API provides a high-level interface for audit workflows.

**Tech Stack:** Go, JSON marshaling, existing `internal/audit` package

---

## Task 1: Define ProofBundle Type

**Files:**
- Create: `internal/audit/bundle.go`
- Create: `internal/audit/bundle_test.go`

**Step 1: Write the failing test**

Create `internal/audit/bundle_test.go`:

```go
package audit

import (
	"testing"
	"time"
)

func TestProofBundle_Structure(t *testing.T) {
	bundle := &ProofBundle{
		Version:    1,
		CreatedAt:  time.Now().UTC(),
		MerkleRoot: "abc123",
		Entries: []*Entry{
			{Sequence: 1, Type: EntryConsole, Hash: "hash1"},
			{Sequence: 2, Type: EntryConsole, Hash: "hash2"},
		},
		Attestations: []*Attestation{
			{Sequence: 2, RootHash: "abc123"},
		},
	}

	if bundle.Version != 1 {
		t.Errorf("Version = %d, want 1", bundle.Version)
	}
	if len(bundle.Entries) != 2 {
		t.Errorf("Entries length = %d, want 2", len(bundle.Entries))
	}
	if len(bundle.Attestations) != 1 {
		t.Errorf("Attestations length = %d, want 1", len(bundle.Attestations))
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/audit/... -run TestProofBundle_Structure -v
```

Expected: FAIL - ProofBundle undefined

**Step 3: Write minimal implementation**

Create `internal/audit/bundle.go`:

```go
package audit

import "time"

// BundleVersion is the current proof bundle format version.
const BundleVersion = 1

// ProofBundle is a portable, self-contained audit log proof.
// It contains everything needed to verify an audit log without
// access to the original database.
type ProofBundle struct {
	Version     int              `json:"version"`
	CreatedAt   time.Time        `json:"created_at"`
	MerkleRoot  string           `json:"merkle_root"`
	Entries     []*Entry         `json:"entries"`
	Attestations []*Attestation  `json:"attestations,omitempty"`
	RekorProofs  []*RekorProof   `json:"rekor_proofs,omitempty"`
	Proofs      []*InclusionProof `json:"inclusion_proofs,omitempty"`
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/audit/... -run TestProofBundle_Structure -v
```

**Step 5: Commit**

```bash
git add internal/audit/bundle.go internal/audit/bundle_test.go
git commit -m "feat(audit): add ProofBundle type"
```

---

## Task 2: Implement Store.Export()

**Files:**
- Modify: `internal/audit/bundle.go`
- Modify: `internal/audit/bundle_test.go`

**Step 1: Write the failing test**

Add to `internal/audit/bundle_test.go`:

```go
func TestStore_Export(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "test.db"))
	defer store.Close()

	// Add entries
	store.AppendConsole("line 1")
	store.AppendConsole("line 2")
	store.AppendConsole("line 3")

	bundle, err := store.Export()
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	if bundle.Version != BundleVersion {
		t.Errorf("Version = %d, want %d", bundle.Version, BundleVersion)
	}
	if len(bundle.Entries) != 3 {
		t.Errorf("Entries = %d, want 3", len(bundle.Entries))
	}
	if bundle.MerkleRoot == "" {
		t.Error("MerkleRoot should not be empty")
	}
	if bundle.MerkleRoot != store.MerkleRoot() {
		t.Errorf("MerkleRoot mismatch")
	}
}
```

Don't forget to add `"path/filepath"` to imports.

**Step 2: Run test to verify it fails**

```bash
go test ./internal/audit/... -run TestStore_Export -v
```

**Step 3: Add implementation to store.go**

Add to `internal/audit/store.go`:

```go
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
	var rekorProofs []*RekorProof
	for _, p := range rekorProofsMap {
		rekorProofs = append(rekorProofs, p)
	}

	return &ProofBundle{
		Version:      BundleVersion,
		CreatedAt:   time.Now().UTC(),
		MerkleRoot:  s.merkleRoot,
		Entries:     entries,
		Attestations: attestations,
		RekorProofs:  rekorProofs,
	}, nil
}
```

**Step 4: Run test**

```bash
go test ./internal/audit/... -run TestStore_Export -v
```

**Step 5: Commit**

```bash
git add internal/audit/store.go internal/audit/bundle_test.go
git commit -m "feat(audit): add Store.Export() for proof bundles"
```

---

## Task 3: Implement ProofBundle.Verify()

**Files:**
- Modify: `internal/audit/bundle.go`
- Modify: `internal/audit/bundle_test.go`

**Step 1: Write the failing test**

Add to `internal/audit/bundle_test.go`:

```go
func TestProofBundle_Verify_Valid(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "test.db"))

	// Add entries
	for i := 0; i < 5; i++ {
		store.AppendConsole("line")
	}

	// Create attestation
	signer, _ := NewSigner(filepath.Join(dir, "test.key"))
	att := &Attestation{
		Sequence:  5,
		RootHash:  store.MerkleRoot(),
		Timestamp: time.Now().UTC(),
		PublicKey: signer.PublicKey(),
	}
	att.Signature = signer.Sign([]byte(att.RootHash))
	store.SaveAttestation(att)

	bundle, _ := store.Export()
	store.Close()

	// Verify bundle
	result := bundle.Verify()
	if !result.Valid {
		t.Errorf("Expected valid, got error: %s", result.Error)
	}
	if result.EntryCount != 5 {
		t.Errorf("EntryCount = %d, want 5", result.EntryCount)
	}
}

func TestProofBundle_Verify_TamperedEntry(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "test.db"))

	store.AppendConsole("line 1")
	store.AppendConsole("line 2")

	bundle, _ := store.Export()
	store.Close()

	// Tamper with an entry
	bundle.Entries[0].Hash = "tampered"

	result := bundle.Verify()
	if result.Valid {
		t.Error("Expected invalid due to tampered entry")
	}
	if result.HashChainValid {
		t.Error("HashChainValid should be false")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/audit/... -run "TestProofBundle_Verify" -v
```

**Step 3: Write implementation**

Add to `internal/audit/bundle.go`:

```go
// Verify performs a full integrity verification on the proof bundle.
// This works offline without access to the original database.
func (b *ProofBundle) Verify() *Result {
	result := &Result{
		Valid:             true,
		HashChainValid:    true,
		MerkleRootValid:   true,
		AttestationsValid: true,
		RekorProofsValid:  true,
		EntryCount:        uint64(len(b.Entries)),
		AttestationCount:  len(b.Attestations),
		RekorProofCount:   len(b.RekorProofs),
	}

	if len(b.Entries) == 0 {
		return result
	}

	// Verify hash chain
	var prevHash string
	for i, entry := range b.Entries {
		// Check sequence is monotonic
		expectedSeq := uint64(i + 1)
		if entry.Sequence != expectedSeq {
			result.Valid = false
			result.HashChainValid = false
			result.Error = fmt.Sprintf("sequence gap: expected %d, got %d", expectedSeq, entry.Sequence)
			return result
		}

		// Check prev_hash links
		if entry.PrevHash != prevHash {
			result.Valid = false
			result.HashChainValid = false
			result.Error = fmt.Sprintf("broken chain at seq %d: prev_hash mismatch", entry.Sequence)
			return result
		}

		// Verify entry hash
		if !entry.Verify() {
			result.Valid = false
			result.HashChainValid = false
			result.Error = fmt.Sprintf("invalid hash at seq %d: entry tampered", entry.Sequence)
			return result
		}

		prevHash = entry.Hash
	}

	// Verify Merkle root
	tree := BuildMerkleTree(b.Entries)
	if tree.RootHash() != b.MerkleRoot {
		result.Valid = false
		result.MerkleRootValid = false
		result.Error = "merkle root mismatch: stored root doesn't match computed root"
		return result
	}

	// Verify attestations
	for _, att := range b.Attestations {
		if !att.Verify() {
			result.Valid = false
			result.AttestationsValid = false
			result.Error = fmt.Sprintf("invalid signature on attestation at seq %d", att.Sequence)
			return result
		}
	}

	return result
}
```

Add `"fmt"` import to bundle.go.

**Step 4: Run tests**

```bash
go test ./internal/audit/... -run "TestProofBundle_Verify" -v
```

**Step 5: Commit**

```bash
git add internal/audit/bundle.go internal/audit/bundle_test.go
git commit -m "feat(audit): add ProofBundle.Verify() for offline verification"
```

---

## Task 4: Implement JSON Marshal/Unmarshal

**Files:**
- Modify: `internal/audit/bundle.go`
- Modify: `internal/audit/bundle_test.go`

**Step 1: Write the failing test**

Add to `internal/audit/bundle_test.go`:

```go
func TestProofBundle_MarshalUnmarshal(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "test.db"))

	store.AppendConsole("line 1")
	store.AppendConsole("line 2")

	bundle, _ := store.Export()
	store.Close()

	// Marshal to JSON
	data, err := bundle.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}

	// Unmarshal back
	var restored ProofBundle
	if err := restored.UnmarshalJSON(data); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}

	// Verify restored bundle
	if restored.MerkleRoot != bundle.MerkleRoot {
		t.Errorf("MerkleRoot mismatch after round-trip")
	}
	if len(restored.Entries) != len(bundle.Entries) {
		t.Errorf("Entries count mismatch: %d != %d", len(restored.Entries), len(bundle.Entries))
	}

	// Verify the restored bundle
	result := restored.Verify()
	if !result.Valid {
		t.Errorf("Restored bundle invalid: %s", result.Error)
	}
}
```

**Step 2: Run test**

```bash
go test ./internal/audit/... -run TestProofBundle_MarshalUnmarshal -v
```

This should pass with default JSON marshaling. If it fails, we need custom methods.

**Step 3: Add explicit methods for clarity** (optional but good practice)

Add to `internal/audit/bundle.go`:

```go
import "encoding/json"

// MarshalJSON serializes the bundle to JSON.
func (b *ProofBundle) MarshalJSON() ([]byte, error) {
	type Alias ProofBundle
	return json.Marshal((*Alias)(b))
}

// UnmarshalJSON deserializes the bundle from JSON.
func (b *ProofBundle) UnmarshalJSON(data []byte) error {
	type Alias ProofBundle
	aux := (*Alias)(b)
	return json.Unmarshal(data, aux)
}
```

**Step 4: Run test**

```bash
go test ./internal/audit/... -run TestProofBundle_MarshalUnmarshal -v
```

**Step 5: Commit**

```bash
git add internal/audit/bundle.go internal/audit/bundle_test.go
git commit -m "feat(audit): add JSON marshal/unmarshal for ProofBundle"
```

---

## Task 5: Add CLI Export Command

**Files:**
- Modify: `cmd/agent/cli/audit.go`

**Step 1: Read existing audit.go**

```bash
cat /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs/cmd/agent/cli/audit.go
```

**Step 2: Add export subcommand**

Add a new `--export` flag or subcommand to the audit command:

```go
var auditExportFile string

func init() {
	auditCmd.Flags().StringVarP(&auditExportFile, "export", "e", "", "Export proof bundle to file (JSON)")
}
```

In `runAudit()`, after verification, add:

```go
// Export if requested
if auditExportFile != "" {
	bundle, err := exportBundle(runID)
	if err != nil {
		return fmt.Errorf("exporting bundle: %w", err)
	}

	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling bundle: %w", err)
	}

	if err := os.WriteFile(auditExportFile, data, 0644); err != nil {
		return fmt.Errorf("writing bundle: %w", err)
	}

	fmt.Printf("\nProof bundle exported to: %s\n", auditExportFile)
}
```

Add helper function:

```go
func exportBundle(runID string) (*audit.ProofBundle, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(homeDir, ".agentops", "runs", runID, "logs.db")

	store, err := audit.OpenStore(dbPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()

	return store.Export()
}
```

Add `"encoding/json"` to imports.

**Step 3: Build and test**

```bash
go build ./cmd/agent
./agent audit --help
```

**Step 4: Commit**

```bash
git add cmd/agent/cli/audit.go
git commit -m "feat(cli): add --export flag to audit command"
```

---

## Task 6: Add CLI Verify Command for Bundle Files

**Files:**
- Modify: `cmd/agent/cli/audit.go`

**Step 1: Add verify-bundle subcommand**

Add a new command to verify a proof bundle file:

```go
var verifyBundleCmd = &cobra.Command{
	Use:   "verify-bundle <file>",
	Short: "Verify a proof bundle file",
	Long:  "Verifies the integrity of an exported proof bundle without the original database.",
	Args:  cobra.ExactArgs(1),
	RunE:  runVerifyBundle,
}

func init() {
	rootCmd.AddCommand(verifyBundleCmd)
}

func runVerifyBundle(cmd *cobra.Command, args []string) error {
	bundlePath := args[0]

	data, err := os.ReadFile(bundlePath)
	if err != nil {
		return fmt.Errorf("reading bundle: %w", err)
	}

	var bundle audit.ProofBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return fmt.Errorf("parsing bundle: %w", err)
	}

	result := bundle.Verify()

	// Output (reuse the output logic from audit command)
	fmt.Println("Proof Bundle Verification")
	fmt.Println("=========================")
	fmt.Printf("Bundle Version: %d\n", bundle.Version)
	fmt.Printf("Created: %s\n", bundle.CreatedAt.Format(time.RFC3339))
	fmt.Printf("Entries: %d\n", result.EntryCount)
	fmt.Println()

	fmt.Println("Hash Chain")
	if result.HashChainValid {
		fmt.Println("  [ok] All entries verified")
	} else {
		fmt.Println("  [FAIL] Hash chain: INVALID")
	}

	fmt.Println()
	fmt.Println("Merkle Tree")
	if result.MerkleRootValid {
		fmt.Printf("  [ok] Root: %s...\n", bundle.MerkleRoot[:16])
	} else {
		fmt.Println("  [FAIL] Merkle root: MISMATCH")
	}

	fmt.Println()
	fmt.Println("Local Signatures")
	if result.AttestationCount == 0 {
		fmt.Println("  - No attestations in bundle")
	} else if result.AttestationsValid {
		fmt.Printf("  [ok] %d attestation(s) verified\n", result.AttestationCount)
	} else {
		fmt.Println("  [FAIL] Attestation signatures: INVALID")
	}

	fmt.Println()
	fmt.Println("External Attestations (Sigstore/Rekor)")
	if result.RekorProofCount == 0 {
		fmt.Println("  - No Rekor proofs in bundle")
	} else {
		fmt.Printf("  [info] %d Rekor proof(s) included\n", result.RekorProofCount)
	}

	fmt.Println()
	fmt.Println("=========================")
	if result.Valid {
		fmt.Println("VERDICT: VALID ✓")
	} else {
		fmt.Printf("VERDICT: TAMPERED ✗ (%s)\n", result.Error)
		return fmt.Errorf("bundle verification failed")
	}

	return nil
}
```

**Step 2: Build and test**

```bash
go build ./cmd/agent
./agent verify-bundle --help
```

**Step 3: Commit**

```bash
git add cmd/agent/cli/audit.go
git commit -m "feat(cli): add verify-bundle command for offline verification"
```

---

## Task 7: Add ExportWithProofs() for Specific Entries

**Files:**
- Modify: `internal/audit/store.go`
- Modify: `internal/audit/bundle_test.go`

**Step 1: Write the failing test**

Add to `internal/audit/bundle_test.go`:

```go
func TestStore_ExportWithProofs(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "test.db"))
	defer store.Close()

	// Add entries
	for i := 0; i < 10; i++ {
		store.AppendConsole(fmt.Sprintf("line %d", i))
	}

	// Export with inclusion proofs for specific entries
	bundle, err := store.ExportWithProofs([]uint64{1, 5, 10})
	if err != nil {
		t.Fatalf("ExportWithProofs: %v", err)
	}

	if len(bundle.Proofs) != 3 {
		t.Errorf("Proofs = %d, want 3", len(bundle.Proofs))
	}

	// Verify each proof
	for _, proof := range bundle.Proofs {
		if !proof.Verify() {
			t.Errorf("Inclusion proof for seq %d invalid", proof.EntrySeq)
		}
	}
}
```

Add `"fmt"` to imports if not present.

**Step 2: Run test to verify it fails**

```bash
go test ./internal/audit/... -run TestStore_ExportWithProofs -v
```

**Step 3: Write implementation**

Add to `internal/audit/store.go`:

```go
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
```

**Step 4: Run test**

```bash
go test ./internal/audit/... -run TestStore_ExportWithProofs -v
```

**Step 5: Commit**

```bash
git add internal/audit/store.go internal/audit/bundle_test.go
git commit -m "feat(audit): add ExportWithProofs() for selective inclusion proofs"
```

---

## Task 8: Integration Test - Full Bundle Workflow

**Files:**
- Modify: `internal/audit/integration_test.go`

**Step 1: Write the integration test**

Add to `internal/audit/integration_test.go`:

```go
func TestIntegration_BundleWorkflow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "logs.db")

	// === PRODUCER: Create and populate store ===
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	// Add various entry types
	for i := 0; i < 20; i++ {
		store.AppendConsole(fmt.Sprintf("log line %d", i))
	}
	store.AppendNetwork(NetworkData{
		Method:     "POST",
		URL:        "https://api.example.com/data",
		StatusCode: 201,
		DurationMs: 250,
	})
	store.AppendCredential(CredentialData{
		Name:   "github",
		Action: "injected",
		Host:   "api.github.com",
	})

	// Create local attestation
	signer, _ := NewSigner(filepath.Join(dir, "run.key"))
	att := &Attestation{
		Sequence:  22,
		RootHash:  store.MerkleRoot(),
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
		RootHash:  store.MerkleRoot(),
		Timestamp: time.Now().UTC(),
		EntryUUID: "test-uuid",
	}
	store.SaveRekorProof(22, rekorProof)

	// Export with proofs for high-value entries
	bundle, err := store.ExportWithProofs([]uint64{21, 22}) // Network + credential
	if err != nil {
		t.Fatalf("ExportWithProofs: %v", err)
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

	// Verify inclusion proofs
	if len(received.Proofs) != 2 {
		t.Errorf("Proofs = %d, want 2", len(received.Proofs))
	}
	for _, proof := range received.Proofs {
		if !proof.Verify() {
			t.Errorf("Inclusion proof for seq %d failed", proof.EntrySeq)
		}
	}
}
```

**Step 2: Run test**

```bash
go test ./internal/audit/... -run TestIntegration_BundleWorkflow -v
```

**Step 3: Commit**

```bash
git add internal/audit/integration_test.go
git commit -m "test(audit): add bundle workflow integration test"
```

---

## Task 9: Final - Run All Tests and Lint

**Step 1: Run all tests with coverage**

```bash
go test ./internal/audit/... -v -cover
```

Expected: All tests pass with good coverage (>80%).

**Step 2: Run linter**

```bash
golangci-lint run ./internal/audit/...
golangci-lint run ./cmd/agent/...
```

Fix any issues found.

**Step 3: Commit any fixes**

```bash
git add .
git commit -m "chore(audit): address linter feedback for Phase 5"
```

---

## Summary

Phase 5 delivers:

| Component | Description |
|-----------|-------------|
| `ProofBundle` | Self-contained audit proof structure |
| `ProofBundle.Verify()` | Offline verification without database |
| `Store.Export()` | Export full audit log to bundle |
| `Store.ExportWithProofs()` | Export with selective inclusion proofs |
| `agent audit --export` | CLI export to JSON file |
| `agent verify-bundle` | CLI offline bundle verification |
| JSON serialization | Portable bundle format |

**Use cases:**

1. **Compliance Audits** - Export proof bundles for external auditors
2. **Incident Response** - Share verifiable logs without database access
3. **Long-term Archival** - Store proof bundles for future verification
4. **API Integration** - Programmatic audit log verification

Next phase: Real Rekor uploads and public verification.
