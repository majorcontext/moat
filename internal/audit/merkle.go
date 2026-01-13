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

// MerkleTree holds the root of a Merkle tree and leaf count.
type MerkleTree struct {
	Root *MerkleNode
	size uint64
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
