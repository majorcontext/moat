package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

// Refresh exchanges the current token for a new long-lived token via Meta's
// fb_exchange_token endpoint and updates the proxy.
func (p *Provider) Refresh(ctx context.Context, proxy provider.ProxyConfigurer, cred *provider.Credential) (*provider.Credential, error) {
	return p.refresh(ctx, proxy, cred, graphAPIBase)
}

// refresh is the internal implementation with a configurable base URL for testing.
func (p *Provider) refresh(ctx context.Context, proxy provider.ProxyConfigurer, cred *provider.Credential, baseURL string) (*provider.Credential, error) {
	if !p.CanRefresh(cred) {
		return nil, provider.ErrRefreshNotSupported
	}

	appID := cred.Metadata[MetaKeyAppID]
	appSecret := cred.Metadata[MetaKeyAppSecret]

	client := &http.Client{Timeout: 10 * time.Second}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	params := url.Values{
		"grant_type":        {"fb_exchange_token"},
		"client_id":         {appID},
		"client_secret":     {appSecret},
		"fb_exchange_token": {cred.Token},
	}

	req, err := http.NewRequestWithContext(reqCtx, "GET", baseURL+"/"+apiVersion()+"/oauth/access_token?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating refresh request: %w", err)
	}
	req.Header.Set("User-Agent", "moat")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token refresh request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if decodeErr := json.NewDecoder(resp.Body).Decode(&errResp); decodeErr == nil && errResp.Error.Message != "" {
			return nil, fmt.Errorf("token refresh failed (%d): %s", resp.StatusCode, errResp.Error.Message)
		}
		return nil, fmt.Errorf("token refresh failed with status %d", resp.StatusCode)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&tokenResp); decodeErr != nil {
		return nil, fmt.Errorf("parsing refresh response: %w", decodeErr)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("empty access_token in refresh response")
	}

	// Update proxy for both hosts
	proxy.SetCredentialWithGrant("graph.facebook.com", "Authorization", "Bearer "+tokenResp.AccessToken, "meta")
	proxy.SetCredentialWithGrant("graph.instagram.com", "Authorization", "Bearer "+tokenResp.AccessToken, "meta")

	// Return updated credential
	updated := *cred
	updated.Token = tokenResp.AccessToken
	if tokenResp.ExpiresIn > 0 {
		updated.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}
	return &updated, nil
}
