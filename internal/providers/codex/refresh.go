package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

const (
	codexTokenURL = "https://auth.openai.com/oauth/token"
	codexClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
)

var codexHTTPClient = &http.Client{Timeout: 30 * time.Second}

func (p *Provider) Refresh(ctx context.Context, _ provider.ProxyConfigurer, cred *provider.Credential) (*provider.Credential, error) {
	auth, err := decodeSubscriptionAuth(cred)
	if err != nil {
		return nil, provider.ErrRefreshNotSupported
	}
	body, err := json.Marshal(map[string]string{
		"client_id": codexClientID, "grant_type": "refresh_token", "refresh_token": auth.RefreshToken,
	})
	if err != nil {
		return nil, fmt.Errorf("encoding Codex refresh request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexTokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating Codex refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := codexHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refreshing Codex subscription: %w", err)
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, 1<<20)
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, limited)
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("%w: Codex subscription refresh was rejected; run 'moat grant codex' again", provider.ErrTokenRevoked)
		}
		return nil, fmt.Errorf("Codex subscription refresh failed with status %d", resp.StatusCode)
	}
	var refreshed struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if decodeErr := json.NewDecoder(limited).Decode(&refreshed); decodeErr != nil {
		return nil, fmt.Errorf("parsing Codex refresh response: %w", decodeErr)
	}
	if refreshed.AccessToken == "" {
		return nil, fmt.Errorf("Codex refresh response omitted access_token")
	}
	updatedAuth := *auth
	updatedAuth.AccessToken = refreshed.AccessToken
	if refreshed.RefreshToken != "" {
		updatedAuth.RefreshToken = refreshed.RefreshToken
	}
	for _, token := range []string{refreshed.IDToken, refreshed.AccessToken} {
		if accountID := jwtAccountID(token); accountID != "" && accountID != auth.AccountID {
			return nil, fmt.Errorf("Codex refresh returned a different account; run 'moat grant codex' again")
		}
	}
	encoded, err := json.Marshal(&updatedAuth)
	if err != nil {
		return nil, fmt.Errorf("encoding refreshed Codex credential: %w", err)
	}
	updated := *cred
	updated.Token = string(encoded)
	updated.ExpiresAt = jwtExpiry(updatedAuth.AccessToken)
	return &updated, nil
}
