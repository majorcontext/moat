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
