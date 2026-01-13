package audit

import (
	"path/filepath"
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

func TestStore_Export(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "test.db"))
	defer store.Close()

	// Add entries
	store.AppendConsole("line 1")
	store.AppendConsole("line 2")
	store.AppendConsole("line 3")

	bundle, err := store.Export()
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	if bundle.Version != BundleVersion {
		t.Errorf("Version = %d, want %d", bundle.Version, BundleVersion)
	}
	if len(bundle.Entries) != 3 {
		t.Errorf("Entries = %d, want 3", len(bundle.Entries))
	}
	if bundle.MerkleRoot == "" {
		t.Error("MerkleRoot should not be empty")
	}
	if bundle.MerkleRoot != store.MerkleRoot() {
		t.Errorf("MerkleRoot mismatch")
	}
}
