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
