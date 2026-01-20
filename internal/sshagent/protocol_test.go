package sshagent

import (
	"strings"
	"testing"
)

func TestFingerprint(t *testing.T) {
	// Test with a sample key blob
	keyBlob := []byte("test-ssh-key-blob-data")

	fp := Fingerprint(keyBlob)
	if fp == "" {
		t.Error("Fingerprint should not be empty")
	}
	if !strings.HasPrefix(fp, "SHA256:") {
		t.Errorf("Fingerprint should start with SHA256:, got %s", fp)
	}
	// SHA256 base64 is 43 chars, plus "SHA256:" prefix = 50
	if len(fp) < 40 {
		t.Errorf("Fingerprint too short: %s", fp)
	}
}

func TestFingerprintDeterministic(t *testing.T) {
	keyBlob := []byte("consistent-key-data")

	fp1 := Fingerprint(keyBlob)
	fp2 := Fingerprint(keyBlob)

	if fp1 != fp2 {
		t.Errorf("Fingerprint should be deterministic: %s != %s", fp1, fp2)
	}
}

func TestFingerprintDifferentKeys(t *testing.T) {
	fp1 := Fingerprint([]byte("key1"))
	fp2 := Fingerprint([]byte("key2"))

	if fp1 == fp2 {
		t.Error("Different keys should have different fingerprints")
	}
}

func TestIdentityFingerprint(t *testing.T) {
	id := &Identity{
		KeyBlob: []byte("test-key-blob"),
		Comment: "test@example.com",
	}

	fp := id.Fingerprint()
	if fp == "" {
		t.Error("Identity.Fingerprint() should not be empty")
	}
	if !strings.HasPrefix(fp, "SHA256:") {
		t.Errorf("Identity.Fingerprint() should start with SHA256:, got %s", fp)
	}
}

func TestAgentClientInterface(t *testing.T) {
	// Verify our interface is implementable
	var _ AgentClient = (*mockAgent)(nil)
}

type mockAgent struct {
	identities []*Identity
	signErr    error
}

func (m *mockAgent) List() ([]*Identity, error) {
	return m.identities, nil
}

func (m *mockAgent) Sign(key *Identity, data []byte) ([]byte, error) {
	if m.signErr != nil {
		return nil, m.signErr
	}
	return []byte("mock-signature"), nil
}

func (m *mockAgent) Close() error {
	return nil
}
