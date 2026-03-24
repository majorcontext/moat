package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGeneratePKCE(t *testing.T) {
	verifier, challenge := generatePKCE()

	// Verifier should be base64url-encoded 32 bytes = 43 characters (no padding).
	if len(verifier) != 43 {
		t.Fatalf("expected verifier length 43, got %d", len(verifier))
	}

	// Challenge must equal base64url(sha256(verifier)).
	h := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(h[:])
	if challenge != want {
		t.Fatalf("challenge mismatch:\n  got:  %s\n  want: %s", challenge, want)
	}

	// Two calls should produce different verifiers.
	v2, _ := generatePKCE()
	if verifier == v2 {
		t.Fatal("expected different verifiers on successive calls")
	}
}

func TestBuildAuthURL(t *testing.T) {
	cfg := &Config{
		AuthURL:  "https://auth.example.com/authorize",
		ClientID: "my-client",
	}

	u := buildAuthURL(cfg, "mystate", "mychallenge", "http://localhost:9999/callback", "")
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	q := parsed.Query()
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q, want code", q.Get("response_type"))
	}
	if q.Get("client_id") != "my-client" {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != "http://localhost:9999/callback" {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	if q.Get("code_challenge") != "mychallenge" {
		t.Errorf("code_challenge = %q", q.Get("code_challenge"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q", q.Get("code_challenge_method"))
	}
	if q.Get("state") != "mystate" {
		t.Errorf("state = %q", q.Get("state"))
	}
	// No scope or resource when empty.
	if q.Get("scope") != "" {
		t.Errorf("scope should be absent, got %q", q.Get("scope"))
	}
	if q.Get("resource") != "" {
		t.Errorf("resource should be absent, got %q", q.Get("resource"))
	}
}

func TestBuildAuthURLWithScopesAndResource(t *testing.T) {
	cfg := &Config{
		AuthURL:  "https://auth.example.com/authorize",
		ClientID: "my-client",
		Scopes:   "read write",
	}

	u := buildAuthURL(cfg, "s", "c", "http://localhost/callback", "https://api.example.com")
	parsed, _ := url.Parse(u)
	q := parsed.Query()
	if q.Get("scope") != "read write" {
		t.Errorf("scope = %q, want 'read write'", q.Get("scope"))
	}
	if q.Get("resource") != "https://api.example.com" {
		t.Errorf("resource = %q", q.Get("resource"))
	}
}

func TestCallbackServer(t *testing.T) {
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	srv, port, err := startCallbackServer("goodstate", codeCh, errCh)
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}
	defer srv.Close()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?state=goodstate&code=abc123", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Authorization successful") {
		t.Errorf("unexpected body: %s", body)
	}

	select {
	case code := <-codeCh:
		if code != "abc123" {
			t.Errorf("code = %q, want abc123", code)
		}
	default:
		t.Fatal("expected code on channel")
	}
}

func TestCallbackServerBadState(t *testing.T) {
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	srv, port, err := startCallbackServer("expected", codeCh, errCh)
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}
	defer srv.Close()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?state=wrong&code=abc", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()

	select {
	case e := <-errCh:
		if !strings.Contains(e.Error(), "state") {
			t.Errorf("error should mention state: %v", e)
		}
	default:
		t.Fatal("expected error on channel")
	}
}

func TestCallbackServerErrorParam(t *testing.T) {
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	srv, port, err := startCallbackServer("s", codeCh, errCh)
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}
	defer srv.Close()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?error=access_denied&error_description=user+denied", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()

	select {
	case e := <-errCh:
		if !strings.Contains(e.Error(), "access_denied") {
			t.Errorf("error should contain error code: %v", e)
		}
	default:
		t.Fatal("expected error on channel")
	}
}

func TestExchangeCode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if r.PostForm.Get("grant_type") != "authorization_code" {
			t.Errorf("grant_type = %q", r.PostForm.Get("grant_type"))
		}
		if r.PostForm.Get("code") != "thecode" {
			t.Errorf("code = %q", r.PostForm.Get("code"))
		}
		if r.PostForm.Get("redirect_uri") != "http://localhost/cb" {
			t.Errorf("redirect_uri = %q", r.PostForm.Get("redirect_uri"))
		}
		if r.PostForm.Get("client_id") != "cid" {
			t.Errorf("client_id = %q", r.PostForm.Get("client_id"))
		}
		if r.PostForm.Get("code_verifier") != "myverifier" {
			t.Errorf("code_verifier = %q", r.PostForm.Get("code_verifier"))
		}
		// No client_secret expected.
		if r.PostForm.Get("client_secret") != "" {
			t.Errorf("client_secret should be absent, got %q", r.PostForm.Get("client_secret"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "tok123",
			"refresh_token": "ref456",
			"expires_in":    3600,
			"token_type":    "Bearer",
		})
	}))
	defer ts.Close()

	cfg := &Config{TokenURL: ts.URL, ClientID: "cid"}
	tok, err := exchangeCode(context.Background(), cfg, "thecode", "http://localhost/cb", "myverifier")
	if err != nil {
		t.Fatalf("exchangeCode: %v", err)
	}
	if tok.AccessToken != "tok123" {
		t.Errorf("access_token = %q", tok.AccessToken)
	}
	if tok.RefreshToken != "ref456" {
		t.Errorf("refresh_token = %q", tok.RefreshToken)
	}
	if tok.ExpiresIn != 3600 {
		t.Errorf("expires_in = %d", tok.ExpiresIn)
	}
}

func TestExchangeCodeWithSecret(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostForm.Get("client_secret") != "secret123" {
			t.Errorf("client_secret = %q, want secret123", r.PostForm.Get("client_secret"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "tok",
			"token_type":   "Bearer",
		})
	}))
	defer ts.Close()

	cfg := &Config{TokenURL: ts.URL, ClientID: "cid", ClientSecret: "secret123"}
	tok, err := exchangeCode(context.Background(), cfg, "code", "http://localhost/cb", "v")
	if err != nil {
		t.Fatalf("exchangeCode: %v", err)
	}
	if tok.AccessToken != "tok" {
		t.Errorf("access_token = %q", tok.AccessToken)
	}
}

func TestExchangeCodeNoExpiresIn(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "tok",
			"token_type":   "Bearer",
		})
	}))
	defer ts.Close()

	cfg := &Config{TokenURL: ts.URL, ClientID: "cid"}
	tok, err := exchangeCode(context.Background(), cfg, "code", "http://localhost/cb", "v")
	if err != nil {
		t.Fatalf("exchangeCode: %v", err)
	}
	if tok.ExpiresIn != 0 {
		t.Errorf("expires_in = %d, want 0", tok.ExpiresIn)
	}
}

func TestExchangeCodeError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer ts.Close()

	cfg := &Config{TokenURL: ts.URL, ClientID: "cid"}
	_, err := exchangeCode(context.Background(), cfg, "code", "http://localhost/cb", "v")
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should contain status code: %v", err)
	}
}
