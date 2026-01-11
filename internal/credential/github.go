// Package credential provides credential management for AgentOps.
package credential

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

const (
	githubDeviceCodeURL = "https://github.com/login/device/code"
	githubTokenURL      = "https://github.com/login/oauth/access_token"
)

// GitHubDeviceAuth handles GitHub device flow authentication.
type GitHubDeviceAuth struct {
	ClientID   string
	Scopes     []string
	HTTPClient *http.Client // Optional; uses http.DefaultClient if nil

	// DeviceCodeURL and TokenURL allow overriding endpoints for testing.
	DeviceCodeURL string
	TokenURL      string
}

// httpClient returns the HTTP client to use for requests.
func (g *GitHubDeviceAuth) httpClient() *http.Client {
	if g.HTTPClient != nil {
		return g.HTTPClient
	}
	return http.DefaultClient
}

// deviceCodeURL returns the device code endpoint URL.
func (g *GitHubDeviceAuth) deviceCodeURL() string {
	if g.DeviceCodeURL != "" {
		return g.DeviceCodeURL
	}
	return githubDeviceCodeURL
}

// tokenURL returns the token endpoint URL.
func (g *GitHubDeviceAuth) tokenURL() string {
	if g.TokenURL != "" {
		return g.TokenURL
	}
	return githubTokenURL
}

// DeviceCodeResponse is the response from the device code endpoint.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// TokenResponse is the response from the token endpoint.
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error,omitempty"`
}

// RequestDeviceCode initiates the device flow.
func (g *GitHubDeviceAuth) RequestDeviceCode(ctx context.Context) (*DeviceCodeResponse, error) {
	scope := "repo"
	if len(g.Scopes) > 0 {
		scope = strings.Join(g.Scopes, " ")
	}

	// Use url.Values for proper URL encoding
	data := url.Values{}
	data.Set("client_id", g.ClientID)
	data.Set("scope", scope)

	req, err := http.NewRequestWithContext(ctx, "POST", g.deviceCodeURL(), strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := g.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting device code: %w", err)
	}
	defer resp.Body.Close()

	// Check HTTP status code before decoding
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("device code request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result DeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &result, nil
}

// PollForToken polls for the access token until authorized or timeout.
func (g *GitHubDeviceAuth) PollForToken(ctx context.Context, deviceCode string, interval int) (*TokenResponse, error) {
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			token, err := g.checkToken(ctx, deviceCode)
			if err != nil {
				return nil, err
			}
			if token.AccessToken != "" {
				return token, nil
			}
			if token.Error != "" && token.Error != "authorization_pending" && token.Error != "slow_down" {
				return nil, fmt.Errorf("token error: %s", token.Error)
			}
			if token.Error == "slow_down" {
				// Increase interval
				ticker.Reset(time.Duration(interval+5) * time.Second)
			}
		}
	}
}

func (g *GitHubDeviceAuth) checkToken(ctx context.Context, deviceCode string) (*TokenResponse, error) {
	// Use url.Values for proper URL encoding
	data := url.Values{}
	data.Set("client_id", g.ClientID)
	data.Set("device_code", deviceCode)
	data.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")

	req, err := http.NewRequestWithContext(ctx, "POST", g.tokenURL(), strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := g.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Check HTTP status code before decoding
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}
