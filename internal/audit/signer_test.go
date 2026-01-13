package audit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSigner_GenerateKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "run.key")

	signer, err := NewSigner(keyPath)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}

	// Key file should exist
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		t.Error("Key file should be created")
	}

	// Public key should be available
	if len(signer.PublicKey()) == 0 {
		t.Error("PublicKey should not be empty")
	}
}

func TestSigner_LoadExistingKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "run.key")

	// Create first signer
	signer1, _ := NewSigner(keyPath)
	pubKey1 := signer1.PublicKey()

	// Create second signer with same path - should load existing key
	signer2, _ := NewSigner(keyPath)
	pubKey2 := signer2.PublicKey()

	if string(pubKey1) != string(pubKey2) {
		t.Error("Loading existing key should return same public key")
	}
}
