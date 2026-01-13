package audit

import (
	"testing"
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
