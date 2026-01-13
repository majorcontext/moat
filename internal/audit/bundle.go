package audit

import "time"

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
