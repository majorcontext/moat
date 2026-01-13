package audit

import (
	"context"
	"fmt"
	"time"
)

const defaultRekorURL = "https://rekor.sigstore.dev"

// RekorClient wraps the Sigstore Rekor client for transparency log operations.
type RekorClient struct {
	url string
}

// NewRekorClient creates a new Rekor client.
// If url is empty, uses the default Sigstore instance.
func NewRekorClient(url string) (*RekorClient, error) {
	if url == "" {
		url = defaultRekorURL
	}
	return &RekorClient{url: url}, nil
}

// URL returns the Rekor instance URL.
func (c *RekorClient) URL() string {
	return c.url
}

// RekorProof contains the inclusion proof from Rekor.
type RekorProof struct {
	LogIndex  int64     `json:"log_index"`
	LogID     string    `json:"log_id"`
	TreeSize  int64     `json:"tree_size"`
	RootHash  string    `json:"root_hash"`
	Hashes    []string  `json:"hashes"`
	Timestamp time.Time `json:"timestamp"`
	EntryUUID string    `json:"entry_uuid"`
}

// Upload submits a signed hash to Rekor and returns the inclusion proof.
// This is the core operation for external attestation.
func (c *RekorClient) Upload(ctx context.Context, hash []byte, signature []byte, publicKey []byte) (*RekorProof, error) {
	// Note: Full implementation requires sigstore-go client setup
	// which needs network access. This is a placeholder structure.
	return nil, fmt.Errorf("upload not yet implemented - requires network access to Rekor")
}
