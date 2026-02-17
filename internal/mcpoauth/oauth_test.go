package mcpoauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestBuildAuthURL(t *testing.T) {
	cfg := Config{
		AuthURL:  "https://auth.example.com/authorize",
		ClientID: "test-client-id",
		Scopes:   "read write",
	}

	authURL, err := buildAuthURL(cfg, "http://localhost:8080/callback", "test-state")
	if err != nil {
		t.Fatalf("buildAuthURL failed: %v", err)
	}

	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("failed to parse auth URL: %v", err)
	}

	if u.Host != "auth.example.com" {
		t.Errorf("host = %q, want %q", u.Host, "auth.example.com")
	}
	if u.Path != "/authorize" {
		t.Errorf("path = %q, want %q", u.Path, "/authorize")
	}

	q := u.Query()
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q, want %q", q.Get("response_type"), "code")
	}
	if q.Get("client_id") != "test-client-id" {
		t.Errorf("client_id = %q, want %q", q.Get("client_id"), "test-client-id")
	}
	if q.Get("redirect_uri") != "http://localhost:8080/callback" {
		t.Errorf("redirect_uri = %q, want %q", q.Get("redirect_uri"), "http://localhost:8080/callback")
	}
	if q.Get("state") != "test-state" {
		t.Errorf("state = %q, want %q", q.Get("state"), "test-state")
	}
	if q.Get("scope") != "read write" {
		t.Errorf("scope = %q, want %q", q.Get("scope"), "read write")
	}
}

func TestBuildAuthURL_NoScopes(t *testing.T) {
	cfg := Config{
		AuthURL:  "https://auth.example.com/authorize",
		ClientID: "test-client-id",
	}

	authURL, err := buildAuthURL(cfg, "http://localhost:8080/callback", "test-state")
	if err != nil {
		t.Fatalf("buildAuthURL failed: %v", err)
	}

	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("failed to parse auth URL: %v", err)
	}

	if u.Query().Get("scope") != "" {
		t.Errorf("scope should be empty, got %q", u.Query().Get("scope"))
	}
}

func TestExchangeCode(t *testing.T) {
	// Create a mock token server
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q, want %q", r.Header.Get("Content-Type"), "application/x-www-form-urlencoded")
		}

		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if r.FormValue("grant_type") != "authorization_code" {
			t.Errorf("grant_type = %q, want %q", r.FormValue("grant_type"), "authorization_code")
		}
		if r.FormValue("code") != "test-code" {
			t.Errorf("code = %q, want %q", r.FormValue("code"), "test-code")
		}
		if r.FormValue("client_id") != "test-client" {
			t.Errorf("client_id = %q, want %q", r.FormValue("client_id"), "test-client")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "access-token-123",
			"refresh_token": "refresh-token-456",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer tokenServer.Close()

	cfg := Config{
		TokenURL: tokenServer.URL,
		ClientID: "test-client",
	}

	resp, err := exchangeCode(context.Background(), cfg, "test-code", "http://localhost:8080/callback")
	if err != nil {
		t.Fatalf("exchangeCode failed: %v", err)
	}

	if resp.AccessToken != "access-token-123" {
		t.Errorf("AccessToken = %q, want %q", resp.AccessToken, "access-token-123")
	}
	if resp.RefreshToken != "refresh-token-456" {
		t.Errorf("RefreshToken = %q, want %q", resp.RefreshToken, "refresh-token-456")
	}
	if resp.TokenType != "Bearer" {
		t.Errorf("TokenType = %q, want %q", resp.TokenType, "Bearer")
	}
	if resp.ExpiresAt.IsZero() {
		t.Error("ExpiresAt should not be zero")
	}
	if time.Until(resp.ExpiresAt) < 3500*time.Second {
		t.Errorf("ExpiresAt too soon: %v", resp.ExpiresAt)
	}
}

func TestExchangeCode_Error(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "invalid_grant"}`))
	}))
	defer tokenServer.Close()

	cfg := Config{
		TokenURL: tokenServer.URL,
		ClientID: "test-client",
	}

	_, err := exchangeCode(context.Background(), cfg, "bad-code", "http://localhost:8080/callback")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); !contains(got, "HTTP 400") {
		t.Errorf("error = %q, want to contain %q", got, "HTTP 400")
	}
}

func TestRefreshAccessToken(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if r.FormValue("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q, want %q", r.FormValue("grant_type"), "refresh_token")
		}
		if r.FormValue("refresh_token") != "old-refresh-token" {
			t.Errorf("refresh_token = %q, want %q", r.FormValue("refresh_token"), "old-refresh-token")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "new-access-token",
			"refresh_token": "new-refresh-token",
			"token_type":    "Bearer",
			"expires_in":    7200,
		})
	}))
	defer tokenServer.Close()

	resp, err := RefreshAccessToken(context.Background(), tokenServer.URL, "test-client", "old-refresh-token")
	if err != nil {
		t.Fatalf("RefreshAccessToken failed: %v", err)
	}

	if resp.AccessToken != "new-access-token" {
		t.Errorf("AccessToken = %q, want %q", resp.AccessToken, "new-access-token")
	}
	if resp.RefreshToken != "new-refresh-token" {
		t.Errorf("RefreshToken = %q, want %q", resp.RefreshToken, "new-refresh-token")
	}
}

func TestRefreshAccessToken_Error(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "invalid_grant"}`))
	}))
	defer tokenServer.Close()

	_, err := RefreshAccessToken(context.Background(), tokenServer.URL, "test-client", "expired-token")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); !contains(got, "HTTP 401") {
		t.Errorf("error = %q, want to contain %q", got, "HTTP 401")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsImpl(s, substr)
}

func containsImpl(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
