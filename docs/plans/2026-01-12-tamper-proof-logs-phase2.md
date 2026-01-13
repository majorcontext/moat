# Tamper-Proof Logs Phase 2: Merkle Tree Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add Merkle tree organization to the audit log for efficient subset proofs and range verification.

**Architecture:** Entries are organized into a binary Merkle tree where leaf nodes contain entry hashes and internal nodes contain SHA-256(left || right). The tree enables O(log n) inclusion proofs for any entry range. The tree root is stored in SQLite and updated on each append.

**Tech Stack:** Go, SHA-256, SQLite (existing `modernc.org/sqlite`)

---

## Task 1: Create MerkleNode Type and Leaf Hash

**Files:**
- Create: `internal/audit/merkle.go`
- Create: `internal/audit/merkle_test.go`

**Step 1: Write the failing test**

Create `internal/audit/merkle_test.go`:

```go
package audit

import (
	"testing"
)

func TestMerkleNode_LeafHash(t *testing.T) {
	// A leaf node's hash is SHA-256 of the entry hash prefixed with 0x00
	entryHash := "abc123def456"

	node := NewLeafNode(1, entryHash)

	if node.EntrySeq != 1 {
		t.Errorf("EntrySeq = %d, want 1", node.EntrySeq)
	}
	if node.Hash == "" {
		t.Error("Hash should not be empty")
	}
	if node.Hash == entryHash {
		t.Error("Leaf hash should differ from entry hash (includes prefix)")
	}
	if node.Left != nil || node.Right != nil {
		t.Error("Leaf node should have no children")
	}
}

func TestMerkleNode_LeafHash_Deterministic(t *testing.T) {
	entryHash := "abc123def456"

	node1 := NewLeafNode(1, entryHash)
	node2 := NewLeafNode(1, entryHash)

	if node1.Hash != node2.Hash {
		t.Error("Same inputs should produce same hash")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestMerkleNode -v
```

Expected: FAIL - NewLeafNode undefined

**Step 3: Write minimal implementation**

Create `internal/audit/merkle.go`:

```go
package audit

import (
	"crypto/sha256"
	"encoding/hex"
)

// MerkleNode represents a node in the Merkle tree.
// Leaf nodes contain entry sequence numbers; internal nodes have children.
type MerkleNode struct {
	Hash     string      `json:"hash"`
	Left     *MerkleNode `json:"left,omitempty"`
	Right    *MerkleNode `json:"right,omitempty"`
	EntrySeq uint64      `json:"seq,omitempty"` // Leaf nodes only (0 for internal)
}

// Domain separation prefixes prevent second-preimage attacks.
const (
	leafPrefix     byte = 0x00
	internalPrefix byte = 0x01
)

// NewLeafNode creates a leaf node from an entry hash.
// Uses domain separation: SHA-256(0x00 || entryHash).
func NewLeafNode(seq uint64, entryHash string) *MerkleNode {
	h := sha256.New()
	h.Write([]byte{leafPrefix})
	h.Write([]byte(entryHash))

	return &MerkleNode{
		Hash:     hex.EncodeToString(h.Sum(nil)),
		EntrySeq: seq,
	}
}
```

**Step 4: Run test to verify it passes**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestMerkleNode -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/audit/merkle.go internal/audit/merkle_test.go
git commit -m "feat(audit): add MerkleNode type with leaf hash computation"
```

---

## Task 2: Add Internal Node Hash Computation

**Files:**
- Modify: `internal/audit/merkle.go`
- Modify: `internal/audit/merkle_test.go`

**Step 1: Write the failing test**

Add to `internal/audit/merkle_test.go`:

```go
func TestMerkleNode_InternalHash(t *testing.T) {
	// An internal node's hash is SHA-256(0x01 || left.hash || right.hash)
	left := NewLeafNode(1, "hash1")
	right := NewLeafNode(2, "hash2")

	node := NewInternalNode(left, right)

	if node.Hash == "" {
		t.Error("Hash should not be empty")
	}
	if node.Left != left {
		t.Error("Left child should be set")
	}
	if node.Right != right {
		t.Error("Right child should be set")
	}
	if node.EntrySeq != 0 {
		t.Error("Internal node should have EntrySeq = 0")
	}
}

func TestMerkleNode_InternalHash_OrderMatters(t *testing.T) {
	left := NewLeafNode(1, "hash1")
	right := NewLeafNode(2, "hash2")

	node1 := NewInternalNode(left, right)
	node2 := NewInternalNode(right, left) // Swapped

	if node1.Hash == node2.Hash {
		t.Error("Different child order should produce different hash")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestMerkleNode_Internal -v
```

Expected: FAIL - NewInternalNode undefined

**Step 3: Write minimal implementation**

Add to `internal/audit/merkle.go`:

```go
// NewInternalNode creates an internal node from two children.
// Uses domain separation: SHA-256(0x01 || left.hash || right.hash).
func NewInternalNode(left, right *MerkleNode) *MerkleNode {
	h := sha256.New()
	h.Write([]byte{internalPrefix})
	h.Write([]byte(left.Hash))
	h.Write([]byte(right.Hash))

	return &MerkleNode{
		Hash:  hex.EncodeToString(h.Sum(nil)),
		Left:  left,
		Right: right,
	}
}
```

**Step 4: Run test to verify it passes**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestMerkleNode_Internal -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/audit/merkle.go internal/audit/merkle_test.go
git commit -m "feat(audit): add internal node hash computation with domain separation"
```

---

## Task 3: Create MerkleTree Type with BuildFromEntries

**Files:**
- Modify: `internal/audit/merkle.go`
- Modify: `internal/audit/merkle_test.go`

**Step 1: Write the failing test**

Add to `internal/audit/merkle_test.go`:

```go
func TestMerkleTree_BuildFromEntries_Empty(t *testing.T) {
	tree := BuildMerkleTree(nil)

	if tree.Root != nil {
		t.Error("Empty tree should have nil root")
	}
	if tree.Size() != 0 {
		t.Errorf("Size = %d, want 0", tree.Size())
	}
}

func TestMerkleTree_BuildFromEntries_Single(t *testing.T) {
	entries := []*Entry{
		{Sequence: 1, Hash: "abc123"},
	}

	tree := BuildMerkleTree(entries)

	if tree.Root == nil {
		t.Fatal("Root should not be nil")
	}
	if tree.Size() != 1 {
		t.Errorf("Size = %d, want 1", tree.Size())
	}
	// Single entry: root is the leaf
	if tree.Root.EntrySeq != 1 {
		t.Errorf("Root.EntrySeq = %d, want 1", tree.Root.EntrySeq)
	}
}

func TestMerkleTree_BuildFromEntries_Multiple(t *testing.T) {
	entries := []*Entry{
		{Sequence: 1, Hash: "hash1"},
		{Sequence: 2, Hash: "hash2"},
		{Sequence: 3, Hash: "hash3"},
		{Sequence: 4, Hash: "hash4"},
	}

	tree := BuildMerkleTree(entries)

	if tree.Root == nil {
		t.Fatal("Root should not be nil")
	}
	if tree.Size() != 4 {
		t.Errorf("Size = %d, want 4", tree.Size())
	}
	// Root should be internal node
	if tree.Root.EntrySeq != 0 {
		t.Error("Root of multi-entry tree should be internal node")
	}
	if tree.Root.Left == nil || tree.Root.Right == nil {
		t.Error("Root should have both children")
	}
}

func TestMerkleTree_BuildFromEntries_Deterministic(t *testing.T) {
	entries := []*Entry{
		{Sequence: 1, Hash: "hash1"},
		{Sequence: 2, Hash: "hash2"},
		{Sequence: 3, Hash: "hash3"},
	}

	tree1 := BuildMerkleTree(entries)
	tree2 := BuildMerkleTree(entries)

	if tree1.Root.Hash != tree2.Root.Hash {
		t.Error("Same entries should produce same root hash")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestMerkleTree_Build -v
```

Expected: FAIL - BuildMerkleTree undefined

**Step 3: Write minimal implementation**

Add to `internal/audit/merkle.go`:

```go
// MerkleTree holds the root of a Merkle tree and leaf count.
type MerkleTree struct {
	Root  *MerkleNode
	size  uint64
}

// Size returns the number of entries in the tree.
func (t *MerkleTree) Size() uint64 {
	return t.size
}

// RootHash returns the root hash, or empty string if tree is empty.
func (t *MerkleTree) RootHash() string {
	if t.Root == nil {
		return ""
	}
	return t.Root.Hash
}

// BuildMerkleTree constructs a Merkle tree from entries.
// Uses a bottom-up approach: create leaf nodes, then combine pairwise.
func BuildMerkleTree(entries []*Entry) *MerkleTree {
	if len(entries) == 0 {
		return &MerkleTree{}
	}

	// Create leaf nodes
	nodes := make([]*MerkleNode, len(entries))
	for i, e := range entries {
		nodes[i] = NewLeafNode(e.Sequence, e.Hash)
	}

	// Build tree bottom-up
	for len(nodes) > 1 {
		var nextLevel []*MerkleNode

		for i := 0; i < len(nodes); i += 2 {
			if i+1 < len(nodes) {
				// Pair exists - create internal node
				nextLevel = append(nextLevel, NewInternalNode(nodes[i], nodes[i+1]))
			} else {
				// Odd node - promote to next level
				nextLevel = append(nextLevel, nodes[i])
			}
		}

		nodes = nextLevel
	}

	return &MerkleTree{
		Root: nodes[0],
		size: uint64(len(entries)),
	}
}
```

**Step 4: Run test to verify it passes**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestMerkleTree_Build -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/audit/merkle.go internal/audit/merkle_test.go
git commit -m "feat(audit): add MerkleTree with BuildFromEntries"
```

---

## Task 4: Add Inclusion Proof Generation

**Files:**
- Modify: `internal/audit/merkle.go`
- Modify: `internal/audit/merkle_test.go`

**Step 1: Write the failing test**

Add to `internal/audit/merkle_test.go`:

```go
func TestMerkleTree_ProveInclusion_SingleEntry(t *testing.T) {
	entries := []*Entry{
		{Sequence: 1, Hash: "hash1"},
	}
	tree := BuildMerkleTree(entries)

	proof, err := tree.ProveInclusion(1)
	if err != nil {
		t.Fatalf("ProveInclusion: %v", err)
	}

	if proof.EntrySeq != 1 {
		t.Errorf("EntrySeq = %d, want 1", proof.EntrySeq)
	}
	if proof.LeafHash == "" {
		t.Error("LeafHash should not be empty")
	}
	if proof.RootHash != tree.RootHash() {
		t.Errorf("RootHash mismatch")
	}
	// Single entry tree has no siblings
	if len(proof.Siblings) != 0 {
		t.Errorf("Siblings = %d, want 0", len(proof.Siblings))
	}
}

func TestMerkleTree_ProveInclusion_FourEntries(t *testing.T) {
	entries := []*Entry{
		{Sequence: 1, Hash: "hash1"},
		{Sequence: 2, Hash: "hash2"},
		{Sequence: 3, Hash: "hash3"},
		{Sequence: 4, Hash: "hash4"},
	}
	tree := BuildMerkleTree(entries)

	// Prove entry 3 (index 2)
	proof, err := tree.ProveInclusion(3)
	if err != nil {
		t.Fatalf("ProveInclusion: %v", err)
	}

	if proof.EntrySeq != 3 {
		t.Errorf("EntrySeq = %d, want 3", proof.EntrySeq)
	}
	// 4-entry tree has height 2, so 2 siblings needed
	if len(proof.Siblings) != 2 {
		t.Errorf("Siblings = %d, want 2", len(proof.Siblings))
	}
}

func TestMerkleTree_ProveInclusion_NotFound(t *testing.T) {
	entries := []*Entry{
		{Sequence: 1, Hash: "hash1"},
		{Sequence: 2, Hash: "hash2"},
	}
	tree := BuildMerkleTree(entries)

	_, err := tree.ProveInclusion(999)
	if err == nil {
		t.Error("Expected error for non-existent entry")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestMerkleTree_ProveInclusion -v
```

Expected: FAIL - ProveInclusion undefined

**Step 3: Write minimal implementation**

Add to `internal/audit/merkle.go`:

```go
import (
	"errors"
	"fmt"
)

// ErrEntryNotFound is returned when an entry is not in the tree.
var ErrEntryNotFound = errors.New("entry not found in tree")

// InclusionProof contains the data needed to verify an entry is in the tree.
type InclusionProof struct {
	EntrySeq uint64        `json:"seq"`
	LeafHash string        `json:"leaf_hash"`
	RootHash string        `json:"root_hash"`
	Siblings []SiblingNode `json:"siblings"`
}

// SiblingNode represents a sibling in the proof path.
type SiblingNode struct {
	Hash    string `json:"hash"`
	IsRight bool   `json:"is_right"` // True if sibling is on the right
}

// ProveInclusion generates a proof that an entry is in the tree.
func (t *MerkleTree) ProveInclusion(seq uint64) (*InclusionProof, error) {
	if t.Root == nil {
		return nil, ErrEntryNotFound
	}

	// Find the leaf and collect siblings along the path
	siblings, leafHash, found := t.collectProofPath(t.Root, seq)
	if !found {
		return nil, ErrEntryNotFound
	}

	return &InclusionProof{
		EntrySeq: seq,
		LeafHash: leafHash,
		RootHash: t.RootHash(),
		Siblings: siblings,
	}, nil
}

// collectProofPath recursively finds the entry and collects sibling hashes.
// Returns (siblings, leafHash, found).
func (t *MerkleTree) collectProofPath(node *MerkleNode, seq uint64) ([]SiblingNode, string, bool) {
	if node == nil {
		return nil, "", false
	}

	// Leaf node - check if it's our target
	if node.Left == nil && node.Right == nil {
		if node.EntrySeq == seq {
			return nil, node.Hash, true
		}
		return nil, "", false
	}

	// Internal node - search children
	if siblings, leafHash, found := t.collectProofPath(node.Left, seq); found {
		// Found in left subtree - add right sibling
		if node.Right != nil {
			siblings = append(siblings, SiblingNode{Hash: node.Right.Hash, IsRight: true})
		}
		return siblings, leafHash, true
	}

	if siblings, leafHash, found := t.collectProofPath(node.Right, seq); found {
		// Found in right subtree - add left sibling
		if node.Left != nil {
			siblings = append(siblings, SiblingNode{Hash: node.Left.Hash, IsRight: false})
		}
		return siblings, leafHash, true
	}

	return nil, "", false
}
```

**Step 4: Run test to verify it passes**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestMerkleTree_ProveInclusion -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/audit/merkle.go internal/audit/merkle_test.go
git commit -m "feat(audit): add inclusion proof generation"
```

---

## Task 5: Add Inclusion Proof Verification

**Files:**
- Modify: `internal/audit/merkle.go`
- Modify: `internal/audit/merkle_test.go`

**Step 1: Write the failing test**

Add to `internal/audit/merkle_test.go`:

```go
func TestInclusionProof_Verify_Valid(t *testing.T) {
	entries := []*Entry{
		{Sequence: 1, Hash: "hash1"},
		{Sequence: 2, Hash: "hash2"},
		{Sequence: 3, Hash: "hash3"},
		{Sequence: 4, Hash: "hash4"},
	}
	tree := BuildMerkleTree(entries)

	// Generate and verify proof for each entry
	for _, e := range entries {
		proof, err := tree.ProveInclusion(e.Sequence)
		if err != nil {
			t.Fatalf("ProveInclusion(%d): %v", e.Sequence, err)
		}

		if !proof.Verify() {
			t.Errorf("Proof for entry %d should verify", e.Sequence)
		}
	}
}

func TestInclusionProof_Verify_TamperedLeaf(t *testing.T) {
	entries := []*Entry{
		{Sequence: 1, Hash: "hash1"},
		{Sequence: 2, Hash: "hash2"},
	}
	tree := BuildMerkleTree(entries)

	proof, _ := tree.ProveInclusion(1)

	// Tamper with leaf hash
	proof.LeafHash = "tampered"

	if proof.Verify() {
		t.Error("Tampered proof should not verify")
	}
}

func TestInclusionProof_Verify_TamperedSibling(t *testing.T) {
	entries := []*Entry{
		{Sequence: 1, Hash: "hash1"},
		{Sequence: 2, Hash: "hash2"},
	}
	tree := BuildMerkleTree(entries)

	proof, _ := tree.ProveInclusion(1)

	// Tamper with sibling
	if len(proof.Siblings) > 0 {
		proof.Siblings[0].Hash = "tampered"
	}

	if proof.Verify() {
		t.Error("Tampered proof should not verify")
	}
}

func TestInclusionProof_Verify_WrongRoot(t *testing.T) {
	entries := []*Entry{
		{Sequence: 1, Hash: "hash1"},
		{Sequence: 2, Hash: "hash2"},
	}
	tree := BuildMerkleTree(entries)

	proof, _ := tree.ProveInclusion(1)

	// Change expected root
	proof.RootHash = "wrongroot"

	if proof.Verify() {
		t.Error("Proof with wrong root should not verify")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestInclusionProof_Verify -v
```

Expected: FAIL - Verify method undefined

**Step 3: Write minimal implementation**

Add to `internal/audit/merkle.go`:

```go
// Verify checks that the proof correctly proves inclusion in the claimed root.
func (p *InclusionProof) Verify() bool {
	// Start with the leaf hash
	currentHash := p.LeafHash

	// Walk up the tree using siblings
	for _, sibling := range p.Siblings {
		h := sha256.New()
		h.Write([]byte{internalPrefix})

		if sibling.IsRight {
			// Sibling is on right: hash(current || sibling)
			h.Write([]byte(currentHash))
			h.Write([]byte(sibling.Hash))
		} else {
			// Sibling is on left: hash(sibling || current)
			h.Write([]byte(sibling.Hash))
			h.Write([]byte(currentHash))
		}

		currentHash = hex.EncodeToString(h.Sum(nil))
	}

	// Compare computed root with claimed root
	return currentHash == p.RootHash
}
```

**Step 4: Run test to verify it passes**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestInclusionProof_Verify -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/audit/merkle.go internal/audit/merkle_test.go
git commit -m "feat(audit): add inclusion proof verification"
```

---

## Task 6: Add MerkleRoot Storage to Store

**Files:**
- Modify: `internal/audit/store.go`
- Modify: `internal/audit/store_test.go`

**Step 1: Write the failing test**

Add to `internal/audit/store_test.go`:

```go
func TestStore_MerkleRoot_Empty(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	root := store.MerkleRoot()
	if root != "" {
		t.Errorf("MerkleRoot = %q, want empty for empty store", root)
	}
}

func TestStore_MerkleRoot_AfterAppend(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	store.Append(EntryConsole, map[string]any{"line": "test"})

	root := store.MerkleRoot()
	if root == "" {
		t.Error("MerkleRoot should not be empty after append")
	}
}

func TestStore_MerkleRoot_ChangesWithEntries(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	store.Append(EntryConsole, map[string]any{"line": "first"})
	root1 := store.MerkleRoot()

	store.Append(EntryConsole, map[string]any{"line": "second"})
	root2 := store.MerkleRoot()

	if root1 == root2 {
		t.Error("MerkleRoot should change when entries are added")
	}
}

func TestStore_MerkleRoot_PersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Create store and add entries
	store1, _ := OpenStore(dbPath)
	store1.Append(EntryConsole, map[string]any{"line": "test"})
	root1 := store1.MerkleRoot()
	store1.Close()

	// Reopen and check root
	store2, _ := OpenStore(dbPath)
	defer store2.Close()
	root2 := store2.MerkleRoot()

	if root1 != root2 {
		t.Errorf("MerkleRoot changed after reopen: %q != %q", root1, root2)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestStore_MerkleRoot -v
```

Expected: FAIL - MerkleRoot method undefined

**Step 3: Write minimal implementation**

Modify `internal/audit/store.go`:

1. Add `merkleRoot` field to Store struct:
```go
type Store struct {
	db         *sql.DB
	mu         sync.Mutex
	lastHash   string
	lastSeq    uint64
	merkleRoot string // Current Merkle tree root hash
}
```

2. Update `createTables` to add metadata table:
```go
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
	`)
	return err
}
```

3. Update `loadLastEntry` to also load merkle root:
```go
func (s *Store) loadLastEntry() error {
	// Load last entry state
	row := s.db.QueryRow(`
		SELECT seq, hash FROM entries ORDER BY seq DESC LIMIT 1
	`)
	var seq uint64
	var hash string
	err := row.Scan(&seq, &hash)
	if err == sql.ErrNoRows {
		// Empty store
	} else if err != nil {
		return fmt.Errorf("loading last entry: %w", err)
	} else {
		s.lastSeq = seq
		s.lastHash = hash
	}

	// Load merkle root
	row = s.db.QueryRow(`SELECT value FROM metadata WHERE key = 'merkle_root'`)
	var root string
	err = row.Scan(&root)
	if err == sql.ErrNoRows {
		// No root yet
	} else if err != nil {
		return fmt.Errorf("loading merkle root: %w", err)
	} else {
		s.merkleRoot = root
	}

	return nil
}
```

4. Update `Append` to rebuild merkle tree:
```go
// At end of Append, after updating lastSeq/lastHash:
	s.rebuildMerkleRoot()

	return entry, nil
}

// rebuildMerkleRoot rebuilds the tree from all entries and saves root.
func (s *Store) rebuildMerkleRoot() {
	entries, err := s.Range(1, s.lastSeq)
	if err != nil {
		return
	}

	tree := BuildMerkleTree(entries)
	s.merkleRoot = tree.RootHash()

	// Persist to metadata
	s.db.Exec(`
		INSERT INTO metadata (key, value) VALUES ('merkle_root', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, s.merkleRoot)
}
```

5. Add `MerkleRoot` method:
```go
// MerkleRoot returns the current Merkle tree root hash.
func (s *Store) MerkleRoot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.merkleRoot
}
```

**Step 4: Run test to verify it passes**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestStore_MerkleRoot -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/audit/store.go internal/audit/store_test.go
git commit -m "feat(audit): add MerkleRoot storage and persistence"
```

---

## Task 7: Add Store.ProveEntry Method

**Files:**
- Modify: `internal/audit/store.go`
- Modify: `internal/audit/store_test.go`

**Step 1: Write the failing test**

Add to `internal/audit/store_test.go`:

```go
func TestStore_ProveEntry(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	// Add several entries
	for i := 0; i < 5; i++ {
		store.Append(EntryConsole, map[string]any{"line": i})
	}

	// Generate proof for entry 3
	proof, err := store.ProveEntry(3)
	if err != nil {
		t.Fatalf("ProveEntry: %v", err)
	}

	if proof.EntrySeq != 3 {
		t.Errorf("EntrySeq = %d, want 3", proof.EntrySeq)
	}
	if proof.RootHash != store.MerkleRoot() {
		t.Error("Proof root should match store's merkle root")
	}
	if !proof.Verify() {
		t.Error("Proof should verify")
	}
}

func TestStore_ProveEntry_NotFound(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	store.Append(EntryConsole, map[string]any{"line": "test"})

	_, err := store.ProveEntry(999)
	if err == nil {
		t.Error("Expected error for non-existent entry")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestStore_ProveEntry -v
```

Expected: FAIL - ProveEntry undefined

**Step 3: Write minimal implementation**

Add to `internal/audit/store.go`:

```go
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
```

**Step 4: Run test to verify it passes**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestStore_ProveEntry -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/audit/store.go internal/audit/store_test.go
git commit -m "feat(audit): add ProveEntry for generating inclusion proofs"
```

---

## Task 8: Integration Test - Merkle Proofs

**Files:**
- Modify: `internal/audit/integration_test.go`

**Step 1: Write the integration test**

Add to `internal/audit/integration_test.go`:

```go
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
```

**Step 2: Run test**

```bash
cd /Users/andybons/dev/agentops/.worktrees/tamper-proof-logs
go test ./internal/audit/... -run TestIntegration_MerkleProofs -v
```

Expected: PASS

**Step 3: Commit**

```bash
git add internal/audit/integration_test.go
git commit -m "test(audit): add Merkle proof integration test"
```

---

## Task 9: Final - Run All Tests and Lint

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
git commit -m "chore(audit): address linter feedback for Phase 2"
```

---

## Summary

Phase 2 delivers:

| Component | Description |
|-----------|-------------|
| `MerkleNode` | Tree node with domain-separated hashing |
| `MerkleTree` | Build tree from entries, get root |
| `InclusionProof` | Proof generation and verification |
| `Store.MerkleRoot()` | Persistent root hash |
| `Store.ProveEntry()` | Generate proofs from store |

Next phase: Local signing and verification CLI.
