// Package sshagent implements a filtering SSH agent proxy.
package sshagent

import (
	"crypto/sha256"
	"encoding/base64"
)

// Identity represents an SSH key identity from the agent.
type Identity struct {
	KeyBlob []byte
	Comment string
}

// Fingerprint returns the SHA256 fingerprint of the key.
func (id *Identity) Fingerprint() string {
	return Fingerprint(id.KeyBlob)
}

// Fingerprint computes the SHA256 fingerprint of a public key blob.
// Returns the fingerprint in the format "SHA256:<base64>".
func Fingerprint(keyBlob []byte) string {
	hash := sha256.Sum256(keyBlob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(hash[:])
}

// AgentClient is the interface for SSH agent operations.
type AgentClient interface {
	// List returns all identities (public keys) from the agent.
	List() ([]*Identity, error)
	// Sign requests the agent to sign data using the specified key.
	Sign(key *Identity, data []byte) ([]byte, error)
	// Close closes the connection to the agent.
	Close() error
}
