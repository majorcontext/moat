package audit

import (
	"testing"
	"time"
)

func TestRekorClient_NewClient(t *testing.T) {
	// Test with default Sigstore instance
	client, err := NewRekorClient("")
	if err != nil {
		t.Fatalf("NewRekorClient: %v", err)
	}
	if client == nil {
		t.Error("Client should not be nil")
	}
	if client.URL() != "https://rekor.sigstore.dev" {
		t.Errorf("URL = %q, want default sigstore URL", client.URL())
	}
}

func TestRekorClient_NewClient_CustomURL(t *testing.T) {
	client, err := NewRekorClient("https://rekor.example.com")
	if err != nil {
		t.Fatalf("NewRekorClient: %v", err)
	}
	if client.URL() != "https://rekor.example.com" {
		t.Errorf("URL = %q, want custom URL", client.URL())
	}
}

func TestRekorProof_Structure(t *testing.T) {
	// Test that RekorProof has required fields
	proof := &RekorProof{
		LogIndex:  12345,
		LogID:     "c0d23d6ad406973f",
		TreeSize:  98765432,
		RootHash:  "abc123",
		Hashes:    []string{"def456", "789abc"},
		Timestamp: time.Now().UTC(),
	}

	if proof.LogIndex != 12345 {
		t.Errorf("LogIndex = %d, want 12345", proof.LogIndex)
	}
	if len(proof.Hashes) != 2 {
		t.Errorf("Hashes length = %d, want 2", len(proof.Hashes))
	}
}
