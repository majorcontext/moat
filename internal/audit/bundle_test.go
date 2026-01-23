package audit

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestProofBundle_Structure(t *testing.T) {
	bundle := &ProofBundle{
		Version:   1,
		CreatedAt: time.Now().UTC(),
		LastHash:  "abc123",
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
	if bundle.LastHash == "" {
		t.Error("LastHash should not be empty")
	}
	if bundle.LastHash != store.LastHash() {
		t.Errorf("LastHash mismatch")
	}
}

func TestProofBundle_Verify_Valid(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "test.db"))

	// Add entries
	for i := 0; i < 5; i++ {
		store.AppendConsole("line")
	}

	// Create attestation
	signer, _ := NewSigner(filepath.Join(dir, "test.key"))
	att := &Attestation{
		Sequence:  5,
		RootHash:  store.LastHash(),
		Timestamp: time.Now().UTC(),
		PublicKey: signer.PublicKey(),
	}
	att.Signature = signer.Sign([]byte(att.RootHash))
	store.SaveAttestation(att)

	bundle, _ := store.Export()
	store.Close()

	// Verify bundle
	result := bundle.Verify()
	if !result.Valid {
		t.Errorf("Expected valid, got error: %s", result.Error)
	}
	if result.EntryCount != 5 {
		t.Errorf("EntryCount = %d, want 5", result.EntryCount)
	}
}

func TestProofBundle_Verify_TamperedEntry(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "test.db"))

	store.AppendConsole("line 1")
	store.AppendConsole("line 2")

	bundle, _ := store.Export()
	store.Close()

	// Tamper with an entry
	bundle.Entries[0].Hash = "tampered"

	result := bundle.Verify()
	if result.Valid {
		t.Error("Expected invalid due to tampered entry")
	}
	if result.HashChainValid {
		t.Error("HashChainValid should be false")
	}
}

func TestProofBundle_MarshalUnmarshal(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "test.db"))

	store.AppendConsole("line 1")
	store.AppendConsole("line 2")

	bundle, _ := store.Export()
	store.Close()

	// Marshal to JSON
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Unmarshal back
	var restored ProofBundle
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Verify restored bundle
	if restored.LastHash != bundle.LastHash {
		t.Errorf("LastHash mismatch after round-trip")
	}
	if len(restored.Entries) != len(bundle.Entries) {
		t.Errorf("Entries count mismatch: %d != %d", len(restored.Entries), len(bundle.Entries))
	}

	// Verify the restored bundle
	result := restored.Verify()
	if !result.Valid {
		t.Errorf("Restored bundle invalid: %s", result.Error)
	}
}
