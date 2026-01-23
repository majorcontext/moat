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

func TestSigner_SignAndVerify(t *testing.T) {
	dir := t.TempDir()
	signer, _ := NewSigner(filepath.Join(dir, "run.key"))

	message := []byte("chain hash abc123")
	signature := signer.Sign(message)

	if len(signature) == 0 {
		t.Fatal("Signature should not be empty")
	}

	if !signer.Verify(message, signature) {
		t.Error("Signature should verify")
	}
}

func TestSigner_VerifyTampered(t *testing.T) {
	dir := t.TempDir()
	signer, _ := NewSigner(filepath.Join(dir, "run.key"))

	message := []byte("chain hash abc123")
	signature := signer.Sign(message)

	// Tamper with message
	tampered := []byte("chain hash TAMPERED")

	if signer.Verify(tampered, signature) {
		t.Error("Tampered message should not verify")
	}
}

func TestSigner_VerifyWithPublicKeyOnly(t *testing.T) {
	dir := t.TempDir()
	signer, _ := NewSigner(filepath.Join(dir, "run.key"))

	message := []byte("chain hash abc123")
	signature := signer.Sign(message)

	// Verify with only public key (simulates third-party verification)
	valid := VerifySignature(signer.PublicKey(), message, signature)
	if !valid {
		t.Error("Should verify with public key only")
	}
}
