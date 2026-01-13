package audit

import (
	"fmt"
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

// IncrementalMerkleTree tests

func TestIncrementalMerkleTree_Empty(t *testing.T) {
	tree := NewIncrementalMerkleTree()

	if tree.RootHash() != "" {
		t.Error("Empty tree should have empty root hash")
	}
	if tree.Size() != 0 {
		t.Errorf("Size = %d, want 0", tree.Size())
	}
}

func TestIncrementalMerkleTree_SingleEntry(t *testing.T) {
	inc := NewIncrementalMerkleTree()
	inc.Append(1, "hash1")

	batch := BuildMerkleTree([]*Entry{{Sequence: 1, Hash: "hash1"}})

	if inc.RootHash() != batch.RootHash() {
		t.Errorf("Root mismatch for 1 entry:\n  incremental: %s\n  batch: %s",
			inc.RootHash(), batch.RootHash())
	}
	if inc.Size() != 1 {
		t.Errorf("Size = %d, want 1", inc.Size())
	}
}

func TestIncrementalMerkleTree_MatchesBatchBuilder(t *testing.T) {
	// Test various sizes including edge cases
	sizes := []int{1, 2, 3, 4, 5, 7, 8, 15, 16, 17, 31, 32, 33, 100}

	for _, n := range sizes {
		entries := make([]*Entry, n)
		inc := NewIncrementalMerkleTree()

		for i := 0; i < n; i++ {
			hash := fmt.Sprintf("hash%d", i+1)
			entries[i] = &Entry{Sequence: uint64(i + 1), Hash: hash}
			inc.Append(uint64(i+1), hash)
		}

		batch := BuildMerkleTree(entries)

		if inc.RootHash() != batch.RootHash() {
			t.Errorf("Root mismatch for %d entries:\n  incremental: %s\n  batch: %s",
				n, inc.RootHash(), batch.RootHash())
		}
	}
}

func TestIncrementalMerkleTree_IncrementalEquivalence(t *testing.T) {
	// Verify that adding entries one-by-one produces same root as batch
	inc := NewIncrementalMerkleTree()
	var entries []*Entry

	for i := 1; i <= 20; i++ {
		hash := fmt.Sprintf("entry%d", i)
		entries = append(entries, &Entry{Sequence: uint64(i), Hash: hash})
		inc.Append(uint64(i), hash)

		batch := BuildMerkleTree(entries)
		if inc.RootHash() != batch.RootHash() {
			t.Errorf("Root mismatch at size %d:\n  incremental: %s\n  batch: %s",
				i, inc.RootHash(), batch.RootHash())
		}
	}
}

// Benchmarks

// BenchmarkMerkleRebuild_FullRebuild simulates the old O(n) approach:
// rebuild the entire tree from scratch on each append.
func BenchmarkMerkleRebuild_FullRebuild(b *testing.B) {
	sizes := []int{100, 1000, 10000}

	for _, n := range sizes {
		entries := make([]*Entry, n)
		for i := 0; i < n; i++ {
			entries[i] = &Entry{Sequence: uint64(i + 1), Hash: fmt.Sprintf("hash%d", i+1)}
		}

		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				// Simulate one append with full rebuild
				tree := BuildMerkleTree(entries)
				_ = tree.RootHash()
			}
		})
	}
}

// BenchmarkMerkleRebuild_Incremental measures the new O(log n) approach:
// only recompute affected branches when appending.
func BenchmarkMerkleRebuild_Incremental(b *testing.B) {
	sizes := []int{100, 1000, 10000}

	for _, n := range sizes {
		// Pre-populate the tree
		tree := NewIncrementalMerkleTree()
		for i := 0; i < n; i++ {
			tree.Append(uint64(i+1), fmt.Sprintf("hash%d", i+1))
		}

		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				// Simulate one append
				tree.Append(uint64(n+i+1), fmt.Sprintf("newhash%d", i))
				_ = tree.RootHash()
			}
		})
	}
}

// BenchmarkMerkleAppend_Comparison directly compares append costs.
func BenchmarkMerkleAppend_Comparison(b *testing.B) {
	// Start with a tree of 10000 entries and measure append cost
	n := 10000

	b.Run("FullRebuild", func(b *testing.B) {
		entries := make([]*Entry, n)
		for i := 0; i < n; i++ {
			entries[i] = &Entry{Sequence: uint64(i + 1), Hash: fmt.Sprintf("hash%d", i+1)}
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Add one entry and rebuild entire tree
			newEntries := append(entries, &Entry{Sequence: uint64(n + i + 1), Hash: fmt.Sprintf("new%d", i)})
			tree := BuildMerkleTree(newEntries)
			_ = tree.RootHash()
		}
	})

	b.Run("Incremental", func(b *testing.B) {
		tree := NewIncrementalMerkleTree()
		for i := 0; i < n; i++ {
			tree.Append(uint64(i+1), fmt.Sprintf("hash%d", i+1))
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			tree.Append(uint64(n+i+1), fmt.Sprintf("new%d", i))
			_ = tree.RootHash()
		}
	})
}

