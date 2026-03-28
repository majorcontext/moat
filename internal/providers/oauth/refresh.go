package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

const (
	refreshBuffer = 10 * time.Minute

	// maxResponseBytes caps token endpoint response bodies to prevent
	// memory exhaustion from misbehaving servers.
	maxResponseBytes = 1 << 20 // 1 MB
)

// refreshClient is the shared HTTP client for token refresh requests.
// Reusing the client enables TLS connection pooling across refresh cycles.
var refreshClient = &http.Client{
	Timeout: 30 * time.Second,
}

// refreshToken refreshes an OAuth access token using the refresh_token grant.
// If the token is not near expiry, it returns the credential unchanged.
func refreshToken(ctx context.Context, cred *provider.Credential) (*provider.Credential, error) {
	// If token is not near expiry, return unchanged.
	if !cred.ExpiresAt.IsZero() && time.Until(cred.ExpiresAt) > refreshBuffer {
		return cred, nil
	}

	// Read required metadata.
	tokenURL := cred.Metadata["token_url"]
	if tokenURL == "" {
		return nil, fmt.Errorf("oauth refresh: missing token_url in credential metadata")
	}
	clientID := cred.Metadata["client_id"]
	if clientID == "" {
		return nil, fmt.Errorf("oauth refresh: missing client_id in credential metadata")
	}
	refreshTok := cred.Metadata["refresh_token"]
	if refreshTok == "" {
		return nil, fmt.Errorf("oauth refresh: missing refresh_token in credential metadata")
	}

	// Build form data.
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshTok},
		"client_id":     {clientID},
	}
	if secret := cred.Metadata["client_secret"]; secret != "" {
		form.Set("client_secret", secret)
	}
	if resource := cred.Metadata["resource"]; resource != "" {
		form.Set("resource", resource)
	}

	// POST to token endpoint, honoring the caller's context for cancellation.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("oauth refresh: creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := refreshClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth refresh: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("oauth refresh: reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth refresh: server returned %d: %s", resp.StatusCode, body)
	}

	// Parse JSON response.
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("oauth refresh: parsing response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("oauth refresh: empty access_token in response")
	}

	// Copy metadata to avoid mutating original.
	newMeta := make(map[string]string, len(cred.Metadata))
	for k, v := range cred.Metadata {
		newMeta[k] = v
	}

	// Update refresh token if a new one was returned.
	if tokenResp.RefreshToken != "" {
		newMeta["refresh_token"] = tokenResp.RefreshToken
	}

	// Build updated credential (copy, not mutate).
	expiresAt := cred.ExpiresAt
	if tokenResp.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}
	updated := &provider.Credential{
		Provider:  cred.Provider,
		Token:     tokenResp.AccessToken,
		Scopes:    cred.Scopes,
		ExpiresAt: expiresAt,
		CreatedAt: cred.CreatedAt,
		Metadata:  newMeta,
	}

	return updated, nil
}
