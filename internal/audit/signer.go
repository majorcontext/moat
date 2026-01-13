package audit

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
)

// Signer handles Ed25519 signing for audit logs.
type Signer struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
}

// NewSigner creates or loads an Ed25519 keypair.
// If keyPath exists, loads the existing key. Otherwise generates a new one.
func NewSigner(keyPath string) (*Signer, error) {
	// Try to load existing key
	if data, err := os.ReadFile(keyPath); err == nil {
		block, _ := pem.Decode(data)
		if block == nil || block.Type != "PRIVATE KEY" {
			return nil, fmt.Errorf("invalid key file format")
		}
		privateKey := ed25519.PrivateKey(block.Bytes)
		return &Signer{
			privateKey: privateKey,
			publicKey:  privateKey.Public().(ed25519.PublicKey),
		}, nil
	}

	// Generate new keypair
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating key: %w", err)
	}

	// Save private key
	block := &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateKey,
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0600); err != nil {
		return nil, fmt.Errorf("saving key: %w", err)
	}

	return &Signer{
		privateKey: privateKey,
		publicKey:  publicKey,
	}, nil
}

// PublicKey returns the public key bytes.
func (s *Signer) PublicKey() []byte {
	return s.publicKey
}

// Sign signs a message and returns the signature.
func (s *Signer) Sign(message []byte) []byte {
	return ed25519.Sign(s.privateKey, message)
}

// Verify checks if a signature is valid for the message.
func (s *Signer) Verify(message, signature []byte) bool {
	return ed25519.Verify(s.publicKey, message, signature)
}

// VerifySignature verifies a signature using only the public key.
// This is useful for third-party verification without the private key.
func VerifySignature(publicKey, message, signature []byte) bool {
	if len(publicKey) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(publicKey, message, signature)
}
