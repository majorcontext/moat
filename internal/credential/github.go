// Package credential provides credential management for AgentOps.
package credential

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	githubDeviceCodeURL = "https://github.com/login/device/code"
	githubTokenURL      = "https://github.com/login/oauth/access_token"
)

// GitHubDeviceAuth handles GitHub device flow authentication.
type GitHubDeviceAuth struct {
	ClientID string
	Scopes   []string
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
		scope = ""
		for i, s := range g.Scopes {
			if i > 0 {
				scope += " "
			}
			scope += s
		}
	}

	body := fmt.Sprintf("client_id=%s&scope=%s", g.ClientID, scope)
	req, err := http.NewRequestWithContext(ctx, "POST", githubDeviceCodeURL, bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting device code: %w", err)
	}
	defer resp.Body.Close()

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
	body := fmt.Sprintf("client_id=%s&device_code=%s&grant_type=urn:ietf:params:oauth:grant-type:device_code", g.ClientID, deviceCode)
	req, err := http.NewRequestWithContext(ctx, "POST", githubTokenURL, bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}
