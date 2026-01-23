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
	Version      int            `json:"version"`
	CreatedAt    time.Time      `json:"created_at"`
	LastHash     string         `json:"last_hash"`
	Entries      []*Entry       `json:"entries"`
	Attestations []*Attestation `json:"attestations,omitempty"`
	RekorProofs  []*RekorProof  `json:"rekor_proofs,omitempty"`
}

// Verify performs a full integrity verification on the proof bundle.
// This works offline without access to the original database.
func (b *ProofBundle) Verify() *Result {
	result := &Result{
		Valid:              true,
		HashChainValid:     true,
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
		expectedSeq := uint64(i) + FirstSequence //nolint:gosec // i is bounded by slice length
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

	// Verify last hash matches
	lastEntry := b.Entries[len(b.Entries)-1]
	if lastEntry.Hash != b.LastHash {
		result.Valid = false
		result.HashChainValid = false
		result.Error = "last hash mismatch: stored hash doesn't match computed hash"
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
