package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

// ErrEntryNotFound is returned when an entry is not in the tree.
var ErrEntryNotFound = errors.New("entry not found in tree")

// IncrementalMerkleTree supports O(log n) append operations.
// It maintains a "frontier" of subtree roots at each level, avoiding full tree rebuilds.
// The final root hash is identical to what BuildMerkleTree produces.
type IncrementalMerkleTree struct {
	// frontier[i] holds the root of a complete subtree with 2^i leaves,
	// or nil if no such subtree exists at that level.
	frontier []*MerkleNode
	size     uint64
}

// NewIncrementalMerkleTree creates an empty incremental tree.
func NewIncrementalMerkleTree() *IncrementalMerkleTree {
	return &IncrementalMerkleTree{}
}

// Append adds a new leaf to the tree in O(log n) time.
func (t *IncrementalMerkleTree) Append(seq uint64, entryHash string) {
	node := NewLeafNode(seq, entryHash)
	t.size++

	level := 0
	// Combine with existing subtrees until we find an empty slot
	for level < len(t.frontier) && t.frontier[level] != nil {
		// Combine: existing subtree on left, new node on right
		node = NewInternalNode(t.frontier[level], node)
		t.frontier[level] = nil
		level++
	}

	// Store at the first empty level
	if level >= len(t.frontier) {
		t.frontier = append(t.frontier, node)
	} else {
		t.frontier[level] = node
	}
}

// RootHash computes the current root hash.
// If the tree has an odd structure (non-power-of-2 size), combines subtrees right-to-left.
func (t *IncrementalMerkleTree) RootHash() string {
	if t.size == 0 {
		return ""
	}

	// Combine all non-nil subtrees from smallest (rightmost) to largest
	var root *MerkleNode
	for _, node := range t.frontier {
		if node == nil {
			continue
		}
		if root == nil {
			root = node
		} else {
			// Larger subtree on left, accumulated root on right
			root = NewInternalNode(node, root)
		}
	}

	if root == nil {
		return ""
	}
	return root.Hash
}

// Size returns the number of leaves in the tree.
func (t *IncrementalMerkleTree) Size() uint64 {
	return t.size
}

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
