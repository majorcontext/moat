package meta

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func TestRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v21.0/oauth/access_token" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("grant_type") != "fb_exchange_token" {
			t.Errorf("grant_type = %q, want fb_exchange_token", q.Get("grant_type"))
		}
		if q.Get("client_id") != "app-123" {
			t.Errorf("client_id = %q, want app-123", q.Get("client_id"))
		}
		if q.Get("client_secret") != "secret-456" {
			t.Errorf("client_secret = %q, want secret-456", q.Get("client_secret"))
		}
		if q.Get("fb_exchange_token") != "old-token" {
			t.Errorf("fb_exchange_token = %q, want old-token", q.Get("fb_exchange_token"))
		}

		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new-long-lived-token",
			"token_type":   "bearer",
			"expires_in":   5184000,
		})
	}))
	defer srv.Close()

	p := &Provider{}
	proxy := newFakeProxy()
	cred := &provider.Credential{
		Token: "old-token",
		Metadata: map[string]string{
			MetaKeyAppID:     "app-123",
			MetaKeyAppSecret: "secret-456",
		},
	}

	updated, err := p.refresh(context.Background(), proxy, cred, srv.URL)
	if err != nil {
		t.Fatalf("Refresh() error: %v", err)
	}

	if updated.Token != "new-long-lived-token" {
		t.Errorf("Token = %q, want new-long-lived-token", updated.Token)
	}

	if updated.ExpiresAt.IsZero() {
		t.Error("ExpiresAt should be set")
	}

	// Verify proxy was updated for both hosts
	for _, host := range []string{"graph.facebook.com", "graph.instagram.com"} {
		got, ok := proxy.credentials[host]
		if !ok {
			t.Fatalf("no credential set for %s", host)
		}
		if got[1] != "Bearer new-long-lived-token" {
			t.Errorf("host %s: value = %q, want Bearer new-long-lived-token", host, got[1])
		}
	}
}

func TestRefreshNoAppCredentials(t *testing.T) {
	p := &Provider{}
	proxy := newFakeProxy()
	cred := &provider.Credential{
		Token:    "some-token",
		Metadata: map[string]string{},
	}

	_, err := p.Refresh(context.Background(), proxy, cred)
	if err != provider.ErrRefreshNotSupported {
		t.Errorf("err = %v, want ErrRefreshNotSupported", err)
	}
}

func TestRefreshAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"message": "Invalid token"},
		})
	}))
	defer srv.Close()

	p := &Provider{}
	proxy := newFakeProxy()
	cred := &provider.Credential{
		Token: "bad-token",
		Metadata: map[string]string{
			MetaKeyAppID:     "app-123",
			MetaKeyAppSecret: "secret-456",
		},
	}

	_, err := p.refresh(context.Background(), proxy, cred, srv.URL)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRefreshEmptyAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "",
			"token_type":   "bearer",
		})
	}))
	defer srv.Close()

	p := &Provider{}
	proxy := newFakeProxy()
	cred := &provider.Credential{
		Token: "old-token",
		Metadata: map[string]string{
			MetaKeyAppID:     "app-123",
			MetaKeyAppSecret: "secret-456",
		},
	}

	_, err := p.refresh(context.Background(), proxy, cred, srv.URL)
	if err == nil {
		t.Fatal("expected error for empty access_token, got nil")
	}
}
