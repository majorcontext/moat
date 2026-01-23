package audit

import (
	"time"
)

// Attestation represents a signed checkpoint of the hash chain.
type Attestation struct {
	Sequence  uint64    `json:"seq"`        // Entry sequence at checkpoint
	RootHash  string    `json:"root_hash"`  // Hash of last entry at this point
	Timestamp time.Time `json:"timestamp"`  // When attestation was created
	Signature []byte    `json:"signature"`  // Ed25519 signature of root hash
	PublicKey []byte    `json:"public_key"` // Signer's public key
}

// Verify checks if the attestation signature is valid.
func (a *Attestation) Verify() bool {
	return VerifySignature(a.PublicKey, []byte(a.RootHash), a.Signature)
}
