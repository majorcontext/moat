package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuthError represents an OAuth error from Google's token endpoint.
// Use errors.As to check for this type and IsRevoked() to detect token revocation.
type OAuthError struct {
	Code        string // OAuth error code: "invalid_grant", "invalid_request", etc.
	Description string // Human-readable description from Google
}

func (e *OAuthError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Description)
	}
	return e.Code
}

// IsRevoked returns true if the error indicates the refresh token was revoked.
func (e *OAuthError) IsRevoked() bool {
	return e.Code == "invalid_grant"
}

// TokenRefresher refreshes Google OAuth2 access tokens using a refresh token.
type TokenRefresher struct {
	TokenURL   string       // Override for testing; empty uses OAuthTokenURL
	HTTPClient *http.Client // Override for testing
}

// RefreshResult holds the result of a token refresh.
type RefreshResult struct {
	AccessToken string
	ExpiresAt   time.Time
}

func (r *TokenRefresher) tokenURL() string {
	if r.TokenURL != "" {
		return r.TokenURL
	}
	return OAuthTokenURL
}

func (r *TokenRefresher) httpClient() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	return http.DefaultClient
}

// Refresh exchanges a refresh token for a new access token.
func (r *TokenRefresher) Refresh(ctx context.Context, refreshToken string) (*RefreshResult, error) {
	form := url.Values{
		"client_id":     {OAuthClientID},
		"client_secret": {OAuthClientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", r.tokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := r.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("making refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &errResp)
		return nil, &OAuthError{
			Code:        errResp.Error,
			Description: errResp.Description,
		}
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing refresh response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("no access token in refresh response")
	}

	return &RefreshResult{
		AccessToken: tokenResp.AccessToken,
		ExpiresAt:   time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
	}, nil
}
