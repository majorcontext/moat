package credential

import (
	"testing"
)

func TestSSHMappingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, []byte("test-encryption-key-32-bytes!!ab"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Add a mapping
	err = store.AddSSHMapping(SSHMapping{
		Host:           "github.com",
		KeyFingerprint: "SHA256:abc123",
		KeyPath:        "~/.ssh/id_ed25519",
	})
	if err != nil {
		t.Fatalf("AddSSHMapping: %v", err)
	}

	// Retrieve mappings
	mappings, err := store.GetSSHMappings()
	if err != nil {
		t.Fatalf("GetSSHMappings: %v", err)
	}
	if len(mappings) != 1 {
		t.Fatalf("got %d mappings, want 1", len(mappings))
	}
	if mappings[0].Host != "github.com" {
		t.Errorf("Host = %s, want github.com", mappings[0].Host)
	}
	if mappings[0].KeyFingerprint != "SHA256:abc123" {
		t.Errorf("KeyFingerprint = %s, want SHA256:abc123", mappings[0].KeyFingerprint)
	}
}

func TestSSHMappingForHosts(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, []byte("test-encryption-key-32-bytes!!ab"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	store.AddSSHMapping(SSHMapping{Host: "github.com", KeyFingerprint: "fp1"})
	store.AddSSHMapping(SSHMapping{Host: "gitlab.com", KeyFingerprint: "fp2"})
	store.AddSSHMapping(SSHMapping{Host: "bitbucket.org", KeyFingerprint: "fp3"})

	mappings, err := store.GetSSHMappingsForHosts([]string{"github.com", "gitlab.com"})
	if err != nil {
		t.Fatalf("GetSSHMappingsForHosts: %v", err)
	}
	if len(mappings) != 2 {
		t.Errorf("got %d mappings, want 2", len(mappings))
	}
}

func TestSSHMappingUpdate(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, []byte("test-encryption-key-32-bytes!!ab"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	store.AddSSHMapping(SSHMapping{Host: "github.com", KeyFingerprint: "fp1"})
	store.AddSSHMapping(SSHMapping{Host: "github.com", KeyFingerprint: "fp2"}) // Update

	mappings, _ := store.GetSSHMappings()
	if len(mappings) != 1 {
		t.Fatalf("got %d mappings, want 1 (should update, not add)", len(mappings))
	}
	if mappings[0].KeyFingerprint != "fp2" {
		t.Errorf("KeyFingerprint = %s, want fp2", mappings[0].KeyFingerprint)
	}
}

func TestSSHMappingDelete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, []byte("test-encryption-key-32-bytes!!ab"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	store.AddSSHMapping(SSHMapping{Host: "github.com", KeyFingerprint: "fp1"})
	store.AddSSHMapping(SSHMapping{Host: "gitlab.com", KeyFingerprint: "fp2"})

	if err := store.RemoveSSHMapping("github.com"); err != nil {
		t.Fatalf("RemoveSSHMapping: %v", err)
	}

	mappings, _ := store.GetSSHMappings()
	if len(mappings) != 1 {
		t.Fatalf("got %d mappings, want 1 after delete", len(mappings))
	}
	if mappings[0].Host != "gitlab.com" {
		t.Errorf("Host = %s, want gitlab.com", mappings[0].Host)
	}
}

func TestSSHMappingEmpty(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, []byte("test-encryption-key-32-bytes!!ab"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Should return empty list, not error
	mappings, err := store.GetSSHMappings()
	if err != nil {
		t.Fatalf("GetSSHMappings: %v", err)
	}
	if len(mappings) != 0 {
		t.Errorf("got %d mappings, want 0", len(mappings))
	}
}

func TestSSHMappingCreatedAt(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, []byte("test-encryption-key-32-bytes!!ab"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	store.AddSSHMapping(SSHMapping{Host: "github.com", KeyFingerprint: "fp1"})

	mappings, _ := store.GetSSHMappings()
	if mappings[0].CreatedAt.IsZero() {
		t.Error("CreatedAt should be set automatically")
	}
}
