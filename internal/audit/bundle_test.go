package audit

import (
	"testing"
	"time"
)

func TestProofBundle_Structure(t *testing.T) {
	bundle := &ProofBundle{
		Version:    1,
		CreatedAt:  time.Now().UTC(),
		MerkleRoot: "abc123",
		Entries: []*Entry{
			{Sequence: 1, Type: EntryConsole, Hash: "hash1"},
			{Sequence: 2, Type: EntryConsole, Hash: "hash2"},
		},
		Attestations: []*Attestation{
			{Sequence: 2, RootHash: "abc123"},
		},
	}

	if bundle.Version != 1 {
		t.Errorf("Version = %d, want 1", bundle.Version)
	}
	if len(bundle.Entries) != 2 {
		t.Errorf("Entries length = %d, want 2", len(bundle.Entries))
	}
	if len(bundle.Attestations) != 1 {
		t.Errorf("Attestations length = %d, want 1", len(bundle.Attestations))
	}
}
