package audit

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAttestation_CreateAndSave(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, _ := OpenStore(dbPath)
	defer store.Close()

	// Add some entries
	store.AppendConsole("test log")
	root := store.MerkleRoot()

	// Create attestation
	signer, _ := NewSigner(filepath.Join(dir, "run.key"))
	att := &Attestation{
		Sequence:  1,
		RootHash:  root,
		Timestamp: time.Now().UTC(),
		PublicKey: signer.PublicKey(),
	}
	att.Signature = signer.Sign([]byte(att.RootHash))

	// Save to store
	err := store.SaveAttestation(att)
	if err != nil {
		t.Fatalf("SaveAttestation: %v", err)
	}
}

func TestAttestation_LoadAll(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, _ := OpenStore(dbPath)
	defer store.Close()

	// Add entries and attestations
	store.AppendConsole("test 1")
	signer, _ := NewSigner(filepath.Join(dir, "run.key"))

	att1 := &Attestation{
		Sequence:  1,
		RootHash:  store.MerkleRoot(),
		Timestamp: time.Now().UTC(),
		PublicKey: signer.PublicKey(),
	}
	att1.Signature = signer.Sign([]byte(att1.RootHash))
	store.SaveAttestation(att1)

	store.AppendConsole("test 2")
	att2 := &Attestation{
		Sequence:  2,
		RootHash:  store.MerkleRoot(),
		Timestamp: time.Now().UTC(),
		PublicKey: signer.PublicKey(),
	}
	att2.Signature = signer.Sign([]byte(att2.RootHash))
	store.SaveAttestation(att2)

	// Load all attestations
	attestations, err := store.LoadAttestations()
	if err != nil {
		t.Fatalf("LoadAttestations: %v", err)
	}

	if len(attestations) != 2 {
		t.Errorf("got %d attestations, want 2", len(attestations))
	}
}

func TestAttestation_Verify(t *testing.T) {
	dir := t.TempDir()
	signer, _ := NewSigner(filepath.Join(dir, "run.key"))

	att := &Attestation{
		Sequence:  1,
		RootHash:  "abc123",
		Timestamp: time.Now().UTC(),
		PublicKey: signer.PublicKey(),
	}
	att.Signature = signer.Sign([]byte(att.RootHash))

	// Valid signature should verify
	if !att.Verify() {
		t.Error("Verify() returned false for valid attestation")
	}

	// Tampered root hash should fail
	att.RootHash = "tampered"
	if att.Verify() {
		t.Error("Verify() returned true for tampered attestation")
	}
}
