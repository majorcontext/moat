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
