package audit

import "fmt"

// Result contains the results of verifying a run's integrity.
type Result struct {
	Valid              bool   `json:"valid"`
	HashChainValid     bool   `json:"hash_chain_valid"`
	AttestationsValid  bool   `json:"attestations_valid"`
	RekorProofsPresent bool   `json:"rekor_proofs_present"` // Presence only; verification requires network
	EntryCount         uint64 `json:"entry_count"`
	AttestationCount   int    `json:"attestation_count"`
	RekorProofCount    int    `json:"rekor_proof_count"`
	Error              string `json:"error,omitempty"`
}

// Auditor verifies the integrity of a run's audit logs.
type Auditor struct {
	store *Store
}

// NewAuditor creates an auditor for the given database path.
func NewAuditor(dbPath string) (*Auditor, error) {
	store, err := OpenStore(dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening store: %w", err)
	}
	return &Auditor{store: store}, nil
}

// Close closes the auditor's store.
func (a *Auditor) Close() error {
	return a.store.Close()
}

// Verify performs a full integrity verification.
func (a *Auditor) Verify() (*Result, error) {
	result := &Result{
		Valid:             true,
		HashChainValid:    true,
		AttestationsValid: true,
	}

	// Verify hash chain
	chainResult, err := a.store.VerifyChain()
	if err != nil {
		return nil, fmt.Errorf("verifying chain: %w", err)
	}
	result.EntryCount = chainResult.EntryCount
	if !chainResult.Valid {
		result.Valid = false
		result.HashChainValid = false
		result.Error = chainResult.Error
		return result, nil
	}

	// Verify attestations
	attestations, err := a.store.LoadAttestations()
	if err != nil {
		return nil, fmt.Errorf("loading attestations: %w", err)
	}
	result.AttestationCount = len(attestations)

	for _, att := range attestations {
		if !att.Verify() {
			result.Valid = false
			result.AttestationsValid = false
			result.Error = fmt.Sprintf("invalid signature on attestation at seq %d", att.Sequence)
			return result, nil
		}
	}

	// Load and count Rekor proofs
	rekorProofs, err := a.store.LoadRekorProofs()
	if err != nil {
		return nil, fmt.Errorf("loading rekor proofs: %w", err)
	}
	result.RekorProofCount = len(rekorProofs)
	result.RekorProofsPresent = len(rekorProofs) > 0

	return result, nil
}
