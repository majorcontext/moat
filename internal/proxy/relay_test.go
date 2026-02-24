package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRelay_ForwardsToTarget(t *testing.T) {
	var receivedPath, receivedMethod, receivedQuery string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedMethod = r.Method
		receivedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("relay response"))
	}))
	defer backend.Close()

	p := NewProxy()
	if err := p.AddRelay("anthropic", backend.URL); err != nil {
		t.Fatalf("AddRelay: %v", err)
	}

	req := httptest.NewRequest("POST", "/relay/anthropic/v1/messages?model=claude", nil)
	rec := httptest.NewRecorder()
	p.handleRelay(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if receivedPath != "/v1/messages" {
		t.Errorf("forwarded path = %q, want %q", receivedPath, "/v1/messages")
	}
	if receivedMethod != "POST" {
		t.Errorf("forwarded method = %q, want POST", receivedMethod)
	}
	if receivedQuery != "model=claude" {
		t.Errorf("forwarded query = %q, want %q", receivedQuery, "model=claude")
	}
	body := rec.Body.String()
	if body != "relay response" {
		t.Errorf("body = %q, want %q", body, "relay response")
	}
}

func TestRelay_InjectsCredentials(t *testing.T) {
	var receivedAuth, receivedBeta string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedBeta = r.Header.Get("anthropic-beta")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	p := NewProxy()
	if err := p.AddRelay("anthropic", backend.URL); err != nil {
		t.Fatalf("AddRelay: %v", err)
	}

	// Register credential for backend hostname (without port, matching real behavior)
	backendHost := strings.TrimPrefix(backend.URL, "http://")
	host, _, _ := strings.Cut(backendHost, ":")
	p.SetCredentialWithGrant(host, "Authorization", "Bearer sk-real-token", "claude")
	p.AddExtraHeader(host, "anthropic-beta", "oauth-2025-04-20")

	req := httptest.NewRequest("POST", "/relay/anthropic/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer placeholder")
	rec := httptest.NewRecorder()
	p.handleRelay(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if receivedAuth != "Bearer sk-real-token" {
		t.Errorf("Authorization = %q, want %q", receivedAuth, "Bearer sk-real-token")
	}
	if receivedBeta != "oauth-2025-04-20" {
		t.Errorf("anthropic-beta = %q, want %q", receivedBeta, "oauth-2025-04-20")
	}
}

func TestRelay_RemovesHeaders(t *testing.T) {
	var receivedAPIKey string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAPIKey = r.Header.Get("x-api-key")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	p := NewProxy()
	if err := p.AddRelay("anthropic", backend.URL); err != nil {
		t.Fatalf("AddRelay: %v", err)
	}

	backendHost := strings.TrimPrefix(backend.URL, "http://")
	host, _, _ := strings.Cut(backendHost, ":")
	p.RemoveRequestHeader(host, "x-api-key")

	req := httptest.NewRequest("POST", "/relay/anthropic/v1/messages", nil)
	req.Header.Set("x-api-key", "should-be-removed")
	rec := httptest.NewRecorder()
	p.handleRelay(rec, req)

	if receivedAPIKey != "" {
		t.Errorf("x-api-key should have been removed, got %q", receivedAPIKey)
	}
}

func TestRelay_UnknownName404(t *testing.T) {
	p := NewProxy()
	if err := p.AddRelay("anthropic", "http://localhost:9999"); err != nil {
		t.Fatalf("AddRelay: %v", err)
	}

	req := httptest.NewRequest("GET", "/relay/unknown/path", nil)
	rec := httptest.NewRecorder()
	p.handleRelay(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Unknown relay endpoint") {
		t.Errorf("body = %q, want message about unknown relay", body)
	}
}

func TestRelay_FiltersProxyHeaders(t *testing.T) {
	var receivedProxyAuth, receivedContentType string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedProxyAuth = r.Header.Get("Proxy-Authorization")
		receivedContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	p := NewProxy()
	if err := p.AddRelay("anthropic", backend.URL); err != nil {
		t.Fatalf("AddRelay: %v", err)
	}

	req := httptest.NewRequest("POST", "/relay/anthropic/v1/messages", nil)
	req.Header.Set("Proxy-Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleRelay(rec, req)

	if receivedProxyAuth != "" {
		t.Errorf("Proxy-Authorization should be filtered, got %q", receivedProxyAuth)
	}
	if receivedContentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", receivedContentType, "application/json")
	}
}

func TestRelay_CopiesResponseHeaders(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Header", "test-value")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	p := NewProxy()
	if err := p.AddRelay("anthropic", backend.URL); err != nil {
		t.Fatalf("AddRelay: %v", err)
	}

	req := httptest.NewRequest("POST", "/relay/anthropic/v1/messages", nil)
	rec := httptest.NewRecorder()
	p.handleRelay(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if rec.Header().Get("X-Custom-Header") != "test-value" {
		t.Errorf("X-Custom-Header = %q, want %q", rec.Header().Get("X-Custom-Header"), "test-value")
	}
}

func TestRelay_UnreachableTarget502(t *testing.T) {
	p := NewProxy()
	// Use a port that's definitely not listening
	if err := p.AddRelay("anthropic", "http://127.0.0.1:1"); err != nil {
		t.Fatalf("AddRelay: %v", err)
	}

	req := httptest.NewRequest("POST", "/relay/anthropic/v1/messages", nil)
	rec := httptest.NewRecorder()
	p.handleRelay(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "connection failed") {
		t.Errorf("body = %q, want connection failed message", body)
	}
}

func TestRelay_NoRelayPath(t *testing.T) {
	// Request to /relay/anthropic with no trailing path
	var receivedPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	p := NewProxy()
	if err := p.AddRelay("anthropic", backend.URL); err != nil {
		t.Fatalf("AddRelay: %v", err)
	}

	req := httptest.NewRequest("GET", "/relay/anthropic", nil)
	rec := httptest.NewRecorder()
	p.handleRelay(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if receivedPath != "" && receivedPath != "/" {
		t.Errorf("forwarded path = %q, want empty or /", receivedPath)
	}
}

func TestRelay_StreamsBody(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the request body
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer backend.Close()

	p := NewProxy()
	if err := p.AddRelay("anthropic", backend.URL); err != nil {
		t.Fatalf("AddRelay: %v", err)
	}

	reqBody := `{"model":"claude","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/relay/anthropic/v1/messages", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleRelay(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != reqBody {
		t.Errorf("response body = %q, want echoed request", rec.Body.String())
	}
}

func TestServeHTTP_ProxiedRequestWithRelayPathBypassesRelay(t *testing.T) {
	// A proxied request (r.URL.Host set) with /relay/ in the path
	// should NOT be handled by the relay — it should fall through
	// to the normal proxy handling (which requires auth if set).
	p := NewProxy()
	p.SetAuthToken("secret-token")
	if err := p.AddRelay("anthropic", "http://should-not-reach:9999"); err != nil {
		t.Fatalf("AddRelay: %v", err)
	}

	// Simulate a proxied request: absolute URL sets r.URL.Host
	req := httptest.NewRequest("GET", "http://evil.com/relay/anthropic/v1/messages", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	// Should hit the auth check (407) since it's a proxied request, not the relay
	if rec.Code != http.StatusProxyAuthRequired {
		t.Errorf("status = %d, want 407 (proxied request should not bypass auth via relay)", rec.Code)
	}
}

func TestAddRelay_Validation(t *testing.T) {
	p := NewProxy()

	tests := []struct {
		name      string
		relayName string
		targetURL string
		wantErr   bool
	}{
		{"valid", "anthropic", "http://localhost:8787", false},
		{"empty name", "", "http://localhost:8787", true},
		{"name with slash", "foo/bar", "http://localhost:8787", true},
		{"name with space", "foo bar", "http://localhost:8787", true},
		{"invalid URL scheme", "anthropic", "ftp://localhost:8787", true},
		{"missing host", "anthropic", "http://", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.AddRelay(tt.relayName, tt.targetURL)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
