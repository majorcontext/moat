package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

func TestRefreshToken(t *testing.T) {
	// Set up a mock token server.
	var receivedForm map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("expected Content-Type application/x-www-form-urlencoded, got %s", ct)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		receivedForm = map[string]string{
			"grant_type":    r.FormValue("grant_type"),
			"refresh_token": r.FormValue("refresh_token"),
			"client_id":     r.FormValue("client_id"),
			"client_secret": r.FormValue("client_secret"),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "new-access-token",
			"refresh_token": "new-refresh-token",
			"expires_in":    3600,
		})
	}))
	defer srv.Close()

	cred := &provider.Credential{
		Provider:  "oauth:myapp",
		Token:     "old-access-token",
		Scopes:    []string{"read", "write"},
		ExpiresAt: time.Now().Add(2 * time.Minute), // Near expiry
		CreatedAt: time.Now().Add(-1 * time.Hour),
		Metadata: map[string]string{
			"token_source":  "oauth",
			"refresh_token": "old-refresh-token",
			"token_url":     srv.URL,
			"client_id":     "my-client-id",
			"client_secret": "my-client-secret",
		},
	}

	updated, err := refreshToken(context.Background(), cred)
	if err != nil {
		t.Fatalf("refreshToken failed: %v", err)
	}

	// Verify form params sent to server.
	if receivedForm["grant_type"] != "refresh_token" {
		t.Errorf("grant_type = %q, want %q", receivedForm["grant_type"], "refresh_token")
	}
	if receivedForm["refresh_token"] != "old-refresh-token" {
		t.Errorf("refresh_token = %q, want %q", receivedForm["refresh_token"], "old-refresh-token")
	}
	if receivedForm["client_id"] != "my-client-id" {
		t.Errorf("client_id = %q, want %q", receivedForm["client_id"], "my-client-id")
	}
	if receivedForm["client_secret"] != "my-client-secret" {
		t.Errorf("client_secret = %q, want %q", receivedForm["client_secret"], "my-client-secret")
	}

	// Verify updated credential.
	if updated.Token != "new-access-token" {
		t.Errorf("Token = %q, want %q", updated.Token, "new-access-token")
	}
	if updated.Metadata["refresh_token"] != "new-refresh-token" {
		t.Errorf("metadata refresh_token = %q, want %q", updated.Metadata["refresh_token"], "new-refresh-token")
	}
	if updated.Provider != cred.Provider {
		t.Errorf("Provider = %q, want %q", updated.Provider, cred.Provider)
	}
	if time.Until(updated.ExpiresAt) < 59*time.Minute {
		t.Errorf("ExpiresAt too soon: %v", updated.ExpiresAt)
	}

	// Verify original credential was not mutated.
	if cred.Token != "old-access-token" {
		t.Errorf("original Token was mutated to %q", cred.Token)
	}
	if cred.Metadata["refresh_token"] != "old-refresh-token" {
		t.Errorf("original metadata refresh_token was mutated to %q", cred.Metadata["refresh_token"])
	}
}

func TestRefreshTokenNotNeeded(t *testing.T) {
	cred := &provider.Credential{
		Provider:  "oauth:myapp",
		Token:     "valid-token",
		ExpiresAt: time.Now().Add(30 * time.Minute), // Not near expiry
		Metadata: map[string]string{
			"token_source":  "oauth",
			"refresh_token": "rt",
			"token_url":     "https://example.com/token",
			"client_id":     "cid",
		},
	}

	updated, err := refreshToken(context.Background(), cred)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated != cred {
		t.Error("expected same credential pointer when refresh not needed")
	}
}

func TestRefreshTokenMissingMetadata(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]string
		wantErr  string
	}{
		{
			name: "missing token_url",
			metadata: map[string]string{
				"client_id":     "cid",
				"refresh_token": "rt",
			},
			wantErr: "missing token_url",
		},
		{
			name: "missing client_id",
			metadata: map[string]string{
				"token_url":     "https://example.com/token",
				"refresh_token": "rt",
			},
			wantErr: "missing client_id",
		},
		{
			name: "missing refresh_token",
			metadata: map[string]string{
				"token_url": "https://example.com/token",
				"client_id": "cid",
			},
			wantErr: "missing refresh_token",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cred := &provider.Credential{
				Provider:  "oauth:test",
				Token:     "tok",
				ExpiresAt: time.Now().Add(-1 * time.Minute), // Expired
				Metadata:  tc.metadata,
			}
			_, err := refreshToken(context.Background(), cred)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if got := err.Error(); !strings.Contains(got, tc.wantErr) {
				t.Errorf("error = %q, want to contain %q", got, tc.wantErr)
			}
		})
	}
}

func TestRefreshTokenServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	cred := &provider.Credential{
		Provider:  "oauth:test",
		Token:     "tok",
		ExpiresAt: time.Now().Add(-1 * time.Minute),
		Metadata: map[string]string{
			"token_source":  "oauth",
			"refresh_token": "rt",
			"token_url":     srv.URL,
			"client_id":     "cid",
		},
	}

	_, err := refreshToken(context.Background(), cred)
	if err == nil {
		t.Fatal("expected error for server error response")
	}
	if got := err.Error(); !strings.Contains(got, "500") {
		t.Errorf("error = %q, want to contain status code 500", got)
	}
}

func TestRefreshTokenWithResource(t *testing.T) {
	var receivedResource string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		receivedResource = r.FormValue("resource")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "new-token",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	cred := &provider.Credential{
		Provider:  "oauth:test",
		Token:     "tok",
		ExpiresAt: time.Now().Add(-1 * time.Minute),
		Metadata: map[string]string{
			"token_source":  "oauth",
			"refresh_token": "rt",
			"token_url":     srv.URL,
			"client_id":     "cid",
			"resource":      "https://api.example.com",
		},
	}

	updated, err := refreshToken(context.Background(), cred)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedResource != "https://api.example.com" {
		t.Errorf("resource = %q, want %q", receivedResource, "https://api.example.com")
	}

	if updated.Token != "new-token" {
		t.Errorf("Token = %q, want %q", updated.Token, "new-token")
	}

	// Refresh token should remain from original since server didn't return new one.
	if updated.Metadata["refresh_token"] != "rt" {
		t.Errorf("metadata refresh_token = %q, want %q", updated.Metadata["refresh_token"], "rt")
	}
}
