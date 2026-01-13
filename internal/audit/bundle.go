package audit

import (
	"fmt"
	"time"
)

// BundleVersion is the current proof bundle format version.
const BundleVersion = 1

// ProofBundle is a portable, self-contained audit log proof.
// It contains everything needed to verify an audit log without
// access to the original database.
type ProofBundle struct {
	Version      int               `json:"version"`
	CreatedAt    time.Time         `json:"created_at"`
	MerkleRoot   string            `json:"merkle_root"`
	Entries      []*Entry          `json:"entries"`
	Attestations []*Attestation    `json:"attestations,omitempty"`
	RekorProofs  []*RekorProof     `json:"rekor_proofs,omitempty"`
	Proofs       []*InclusionProof `json:"inclusion_proofs,omitempty"`
}

// Verify performs a full integrity verification on the proof bundle.
// This works offline without access to the original database.
func (b *ProofBundle) Verify() *Result {
	result := &Result{
		Valid:              true,
		HashChainValid:     true,
		MerkleRootValid:    true,
		AttestationsValid:  true,
		RekorProofsPresent: len(b.RekorProofs) > 0,
		EntryCount:         uint64(len(b.Entries)),
		AttestationCount:   len(b.Attestations),
		RekorProofCount:    len(b.RekorProofs),
	}

	if len(b.Entries) == 0 {
		return result
	}

	// Verify hash chain
	var prevHash string
	for i, entry := range b.Entries {
		// Check sequence is monotonic
		expectedSeq := uint64(i) + 1 //nolint:gosec // i is bounded by slice length
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
