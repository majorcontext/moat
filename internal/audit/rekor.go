package audit

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
