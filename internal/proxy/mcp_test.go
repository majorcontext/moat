package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
)

func TestMCPCredentialInjection(t *testing.T) {
	// Mock credential store
	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"mcp-context7": {
				Provider: "mcp-context7",
				Token:    "real-api-key-123",
			},
		},
	}

	// Mock backend that echoes the API_KEY header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Header.Get("API_KEY")))
	}))
	defer backend.Close()

	// Configure MCP server
	mcpServers := []config.MCPServerConfig{
		{
			Name: "context7",
			URL:  backend.URL, // Use test server URL
			Auth: &config.MCPAuthConfig{
				Grant:  "mcp-context7",
				Header: "API_KEY",
			},
		},
	}

	// Create proxy with MCP configuration
	p := &Proxy{
		credStore:  mockStore,
		mcpServers: mcpServers,
	}

	// Create test request with stub credential
	req := httptest.NewRequest("GET", backend.URL, nil)
	req.Header.Set("API_KEY", "moat-stub-mcp-context7")

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	// Verify real credential was injected
	if rec.Body.String() != "real-api-key-123" {
		t.Errorf("expected body 'real-api-key-123', got %q", rec.Body.String())
	}
}

func TestMCPCredentialInjection_NoMatch(t *testing.T) {
	// Request to non-MCP server should pass through unchanged
	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{},
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Header.Get("API_KEY")))
	}))
	defer backend.Close()

	p := &Proxy{
		credStore:  mockStore,
		mcpServers: []config.MCPServerConfig{},
	}

	req := httptest.NewRequest("GET", backend.URL, nil)
	req.Header.Set("API_KEY", "some-value")

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	// Should pass through unchanged
	if rec.Body.String() != "some-value" {
		t.Errorf("expected body 'some-value', got %q", rec.Body.String())
	}
}

func TestMCPCredentialInjection_NoAuthConfig(t *testing.T) {
	// MCP server without auth config should not inject
	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{},
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Header.Get("API_KEY")))
	}))
	defer backend.Close()

	mcpServers := []config.MCPServerConfig{
		{
			Name: "no-auth-server",
			URL:  backend.URL,
			Auth: nil, // No auth configured
		},
	}

	p := &Proxy{
		credStore:  mockStore,
		mcpServers: mcpServers,
	}

	req := httptest.NewRequest("GET", backend.URL, nil)
	req.Header.Set("API_KEY", "original-value")

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	// Should pass through unchanged since no auth is configured
	if rec.Body.String() != "original-value" {
		t.Errorf("expected body 'original-value', got %q", rec.Body.String())
	}
}

func TestMCPCredentialInjection_MissingCredential(t *testing.T) {
	// Request with stub but missing credential should pass stub through
	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{},
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Header.Get("API_KEY")))
	}))
	defer backend.Close()

	mcpServers := []config.MCPServerConfig{
		{
			Name: "context7",
			URL:  backend.URL,
			Auth: &config.MCPAuthConfig{
				Grant:  "mcp-context7",
				Header: "API_KEY",
			},
		},
	}

	p := &Proxy{
		credStore:  mockStore,
		mcpServers: mcpServers,
	}

	req := httptest.NewRequest("GET", backend.URL, nil)
	req.Header.Set("API_KEY", "moat-stub-mcp-context7")

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	// Should pass through stub since credential is missing
	if rec.Body.String() != "moat-stub-mcp-context7" {
		t.Errorf("expected body 'moat-stub-mcp-context7' (stub passed through), got %q", rec.Body.String())
	}
}

func TestMCPCredentialInjection_NoStubPattern(t *testing.T) {
	// Request to MCP server without stub pattern should pass through
	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"mcp-context7": {
				Provider: "mcp-context7",
				Token:    "real-api-key-123",
			},
		},
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Header.Get("API_KEY")))
	}))
	defer backend.Close()

	mcpServers := []config.MCPServerConfig{
		{
			Name: "context7",
			URL:  backend.URL,
			Auth: &config.MCPAuthConfig{
				Grant:  "mcp-context7",
				Header: "API_KEY",
			},
		},
	}

	p := &Proxy{
		credStore:  mockStore,
		mcpServers: mcpServers,
	}

	req := httptest.NewRequest("GET", backend.URL, nil)
	req.Header.Set("API_KEY", "user-provided-key")

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	// Should pass through user-provided key (no stub pattern)
	if rec.Body.String() != "user-provided-key" {
		t.Errorf("expected body 'user-provided-key', got %q", rec.Body.String())
	}
}

func TestMCPCredentialInjection_NoHeader(t *testing.T) {
	// Request to MCP server without the required header should not inject
	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"mcp-context7": {
				Provider: "mcp-context7",
				Token:    "real-api-key-123",
			},
		},
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Header.Get("API_KEY")))
	}))
	defer backend.Close()

	mcpServers := []config.MCPServerConfig{
		{
			Name: "context7",
			URL:  backend.URL,
			Auth: &config.MCPAuthConfig{
				Grant:  "mcp-context7",
				Header: "API_KEY",
			},
		},
	}

	p := &Proxy{
		credStore:  mockStore,
		mcpServers: mcpServers,
	}

	req := httptest.NewRequest("GET", backend.URL, nil)
	// No API_KEY header set

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	// Should return empty string (no header injected)
	if rec.Body.String() != "" {
		t.Errorf("expected empty body, got %q", rec.Body.String())
	}
}

func TestMCPAuthHelpers(t *testing.T) {
	t.Run("mcpAuthType defaults to token", func(t *testing.T) {
		auth := &config.MCPAuthConfig{Type: ""}
		if got := mcpAuthType(auth); got != "token" {
			t.Errorf("mcpAuthType() = %q, want %q", got, "token")
		}
	})

	t.Run("mcpAuthType returns oauth", func(t *testing.T) {
		auth := &config.MCPAuthConfig{Type: "oauth"}
		if got := mcpAuthType(auth); got != "oauth" {
			t.Errorf("mcpAuthType() = %q, want %q", got, "oauth")
		}
	})

	t.Run("mcpAuthHeader returns configured header", func(t *testing.T) {
		auth := &config.MCPAuthConfig{Header: "X-Custom"}
		if got := mcpAuthHeader(auth); got != "X-Custom" {
			t.Errorf("mcpAuthHeader() = %q, want %q", got, "X-Custom")
		}
	})

	t.Run("mcpAuthHeader defaults to Authorization for oauth", func(t *testing.T) {
		auth := &config.MCPAuthConfig{Type: "oauth"}
		if got := mcpAuthHeader(auth); got != "Authorization" {
			t.Errorf("mcpAuthHeader() = %q, want %q", got, "Authorization")
		}
	})

	t.Run("mcpAuthHeader returns empty for token without header", func(t *testing.T) {
		auth := &config.MCPAuthConfig{Type: "token"}
		if got := mcpAuthHeader(auth); got != "" {
			t.Errorf("mcpAuthHeader() = %q, want %q", got, "")
		}
	})

	t.Run("formatMCPToken adds Bearer for oauth", func(t *testing.T) {
		auth := &config.MCPAuthConfig{Type: "oauth"}
		if got := formatMCPToken(auth, "my-token"); got != "Bearer my-token" {
			t.Errorf("formatMCPToken() = %q, want %q", got, "Bearer my-token")
		}
	})

	t.Run("formatMCPToken returns raw for token type", func(t *testing.T) {
		auth := &config.MCPAuthConfig{Type: "token"}
		if got := formatMCPToken(auth, "my-key"); got != "my-key" {
			t.Errorf("formatMCPToken() = %q, want %q", got, "my-key")
		}
	})

	t.Run("formatMCPToken returns raw for default type", func(t *testing.T) {
		auth := &config.MCPAuthConfig{}
		if got := formatMCPToken(auth, "my-key"); got != "my-key" {
			t.Errorf("formatMCPToken() = %q, want %q", got, "my-key")
		}
	})
}

func TestMCPOAuthCredentialInjection(t *testing.T) {
	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"mcp-notion": {
				Provider: "mcp-notion",
				Token:    "oauth-access-token-123",
			},
		},
	}

	// Mock backend that echoes the Authorization header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Header.Get("Authorization")))
	}))
	defer backend.Close()

	mcpServers := []config.MCPServerConfig{
		{
			Name: "notion",
			URL:  backend.URL,
			Auth: &config.MCPAuthConfig{
				Grant:    "mcp-notion",
				Type:     "oauth",
				ClientID: "test-client",
				AuthURL:  "https://example.com/authorize",
				TokenURL: "https://example.com/token",
			},
		},
	}

	p := &Proxy{
		credStore:  mockStore,
		mcpServers: mcpServers,
	}

	req := httptest.NewRequest("GET", backend.URL, nil)
	req.Header.Set("Authorization", "moat-stub-mcp-notion")

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	// Verify Bearer prefix was added for OAuth
	expected := "Bearer oauth-access-token-123"
	if rec.Body.String() != expected {
		t.Errorf("expected body %q, got %q", expected, rec.Body.String())
	}
}

func TestMCPOAuthRelayInjection(t *testing.T) {
	// Test that handleMCPRelay injects OAuth credentials with Bearer prefix
	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"mcp-notion": {
				Provider: "mcp-notion",
				Token:    "oauth-token-456",
			},
		},
	}

	// Mock MCP backend that echoes the Authorization header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(r.Header.Get("Authorization")))
	}))
	defer backend.Close()

	mcpServers := []config.MCPServerConfig{
		{
			Name: "notion",
			URL:  backend.URL,
			Auth: &config.MCPAuthConfig{
				Grant:    "mcp-notion",
				Type:     "oauth",
				ClientID: "test-client",
				AuthURL:  "https://example.com/authorize",
				TokenURL: "https://example.com/token",
			},
		},
	}

	p := &Proxy{
		credStore:  mockStore,
		mcpServers: mcpServers,
	}

	// Request to MCP relay endpoint
	req := httptest.NewRequest("POST", "http://localhost/mcp/notion", nil)
	rec := httptest.NewRecorder()

	p.handleMCPRelay(rec, req)

	expected := "Bearer oauth-token-456"
	if rec.Body.String() != expected {
		t.Errorf("expected body %q, got %q", expected, rec.Body.String())
	}
}

// mockCredentialStore for testing
type mockCredentialStore struct {
	creds map[credential.Provider]*credential.Credential
}

func (m *mockCredentialStore) Get(p credential.Provider) (*credential.Credential, error) {
	if cred, ok := m.creds[p]; ok {
		return cred, nil
	}
	return nil, fmt.Errorf("credential not found")
}

func (m *mockCredentialStore) Save(c credential.Credential) error {
	m.creds[c.Provider] = &c
	return nil
}

func (m *mockCredentialStore) Delete(p credential.Provider) error {
	delete(m.creds, p)
	return nil
}

func (m *mockCredentialStore) List() ([]credential.Credential, error) {
	list := make([]credential.Credential, 0, len(m.creds))
	for _, c := range m.creds {
		list = append(list, *c)
	}
	return list, nil
}

func TestResolveOAuthToken_ValidToken(t *testing.T) {
	// Token that is not expired should be returned as-is
	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"mcp-notion": {
				Provider:  "mcp-notion",
				Token:     "valid-access-token",
				ExpiresAt: time.Now().Add(10 * time.Minute),
			},
		},
	}

	server := &config.MCPServerConfig{
		Name: "notion",
		Auth: &config.MCPAuthConfig{
			Grant:    "mcp-notion",
			Type:     "oauth",
			TokenURL: "https://example.com/token",
			ClientID: "client-id",
		},
	}

	p := &Proxy{credStore: mockStore}
	cred := mockStore.creds["mcp-notion"]
	token := p.resolveOAuthToken(server, cred)

	if token != "valid-access-token" {
		t.Errorf("resolveOAuthToken() = %q, want %q", token, "valid-access-token")
	}
}

func TestResolveOAuthToken_ExpiredTokenRefreshed(t *testing.T) {
	// Mock a token server that returns a new token
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"refreshed-token","token_type":"Bearer","expires_in":3600}`)
	}))
	defer tokenServer.Close()

	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"mcp-notion": {
				Provider:  "mcp-notion",
				Token:     "expired-token",
				ExpiresAt: time.Now().Add(-5 * time.Minute), // expired
				Metadata: map[string]string{
					"refresh_token": "my-refresh-token",
				},
			},
		},
	}

	server := &config.MCPServerConfig{
		Name: "notion",
		Auth: &config.MCPAuthConfig{
			Grant:    "mcp-notion",
			Type:     "oauth",
			TokenURL: tokenServer.URL,
			ClientID: "client-id",
		},
	}

	p := &Proxy{credStore: mockStore}
	cred := mockStore.creds["mcp-notion"]
	token := p.resolveOAuthToken(server, cred)

	if token != "refreshed-token" {
		t.Errorf("resolveOAuthToken() = %q, want %q", token, "refreshed-token")
	}

	// Verify stored credential was updated
	stored := mockStore.creds["mcp-notion"]
	if stored.Token != "refreshed-token" {
		t.Errorf("stored token = %q, want %q", stored.Token, "refreshed-token")
	}
}

func TestResolveOAuthToken_ExpiredNoRefreshToken(t *testing.T) {
	// Expired token with no refresh token should return the expired token
	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"mcp-notion": {
				Provider:  "mcp-notion",
				Token:     "expired-no-refresh",
				ExpiresAt: time.Now().Add(-5 * time.Minute),
				Metadata:  map[string]string{},
			},
		},
	}

	server := &config.MCPServerConfig{
		Name: "notion",
		Auth: &config.MCPAuthConfig{
			Grant:    "mcp-notion",
			Type:     "oauth",
			TokenURL: "https://example.com/token",
			ClientID: "client-id",
		},
	}

	p := &Proxy{credStore: mockStore}
	cred := mockStore.creds["mcp-notion"]
	token := p.resolveOAuthToken(server, cred)

	if token != "expired-no-refresh" {
		t.Errorf("resolveOAuthToken() = %q, want %q", token, "expired-no-refresh")
	}
}

func TestResolveOAuthToken_ExpiredMissingTokenURL(t *testing.T) {
	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"mcp-notion": {
				Provider:  "mcp-notion",
				Token:     "expired-no-url",
				ExpiresAt: time.Now().Add(-5 * time.Minute),
				Metadata: map[string]string{
					"refresh_token": "my-refresh-token",
				},
			},
		},
	}

	server := &config.MCPServerConfig{
		Name: "notion",
		Auth: &config.MCPAuthConfig{
			Grant: "mcp-notion",
			Type:  "oauth",
			// No TokenURL, no ClientID
		},
	}

	p := &Proxy{credStore: mockStore}
	cred := mockStore.creds["mcp-notion"]
	token := p.resolveOAuthToken(server, cred)

	// Should fall back to expired token since there's no token_url
	if token != "expired-no-url" {
		t.Errorf("resolveOAuthToken() = %q, want %q", token, "expired-no-url")
	}
}

func TestResolveOAuthToken_RefreshFailsFallsBack(t *testing.T) {
	// Token server that returns an error
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer tokenServer.Close()

	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"mcp-notion": {
				Provider:  "mcp-notion",
				Token:     "expired-but-only-option",
				ExpiresAt: time.Now().Add(-5 * time.Minute),
				Metadata: map[string]string{
					"refresh_token": "revoked-refresh-token",
				},
			},
		},
	}

	server := &config.MCPServerConfig{
		Name: "notion",
		Auth: &config.MCPAuthConfig{
			Grant:    "mcp-notion",
			Type:     "oauth",
			TokenURL: tokenServer.URL,
			ClientID: "client-id",
		},
	}

	p := &Proxy{credStore: mockStore}
	cred := mockStore.creds["mcp-notion"]
	token := p.resolveOAuthToken(server, cred)

	// Should fall back to expired token since refresh failed
	if token != "expired-but-only-option" {
		t.Errorf("resolveOAuthToken() = %q, want %q", token, "expired-but-only-option")
	}
}

func TestResolveOAuthToken_WithinBufferRefreshes(t *testing.T) {
	// Token that expires within 60 seconds should trigger refresh
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"buffer-refreshed","token_type":"Bearer","expires_in":3600}`)
	}))
	defer tokenServer.Close()

	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"mcp-notion": {
				Provider:  "mcp-notion",
				Token:     "about-to-expire",
				ExpiresAt: time.Now().Add(30 * time.Second), // within 60s buffer
				Metadata: map[string]string{
					"refresh_token": "my-refresh-token",
				},
			},
		},
	}

	server := &config.MCPServerConfig{
		Name: "notion",
		Auth: &config.MCPAuthConfig{
			Grant:    "mcp-notion",
			Type:     "oauth",
			TokenURL: tokenServer.URL,
			ClientID: "client-id",
		},
	}

	p := &Proxy{credStore: mockStore}
	cred := mockStore.creds["mcp-notion"]
	token := p.resolveOAuthToken(server, cred)

	if token != "buffer-refreshed" {
		t.Errorf("resolveOAuthToken() = %q, want %q", token, "buffer-refreshed")
	}
}

func TestResolveOAuthToken_MetadataFallback(t *testing.T) {
	// When auth config doesn't have token_url/client_id, falls back to credential metadata
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"metadata-refreshed","token_type":"Bearer","expires_in":3600}`)
	}))
	defer tokenServer.Close()

	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"mcp-notion": {
				Provider:  "mcp-notion",
				Token:     "expired-token",
				ExpiresAt: time.Now().Add(-5 * time.Minute),
				Metadata: map[string]string{
					"refresh_token": "my-refresh-token",
					"token_url":     tokenServer.URL,
					"client_id":     "metadata-client-id",
				},
			},
		},
	}

	server := &config.MCPServerConfig{
		Name: "notion",
		Auth: &config.MCPAuthConfig{
			Grant: "mcp-notion",
			Type:  "oauth",
			// No TokenURL, no ClientID - should fall back to metadata
		},
	}

	p := &Proxy{credStore: mockStore}
	cred := mockStore.creds["mcp-notion"]
	token := p.resolveOAuthToken(server, cred)

	if token != "metadata-refreshed" {
		t.Errorf("resolveOAuthToken() = %q, want %q", token, "metadata-refreshed")
	}
}

func TestResolveOAuthToken_NilMetadata(t *testing.T) {
	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"mcp-notion": {
				Provider:  "mcp-notion",
				Token:     "expired-nil-metadata",
				ExpiresAt: time.Now().Add(-5 * time.Minute),
				// Metadata is nil
			},
		},
	}

	server := &config.MCPServerConfig{
		Name: "notion",
		Auth: &config.MCPAuthConfig{
			Grant:    "mcp-notion",
			Type:     "oauth",
			TokenURL: "https://example.com/token",
			ClientID: "client-id",
		},
	}

	p := &Proxy{credStore: mockStore}
	cred := mockStore.creds["mcp-notion"]
	token := p.resolveOAuthToken(server, cred)

	// No refresh token in nil metadata, should return expired token
	if token != "expired-nil-metadata" {
		t.Errorf("resolveOAuthToken() = %q, want %q", token, "expired-nil-metadata")
	}
}

func TestResolveOAuthToken_ZeroExpiresAtRefreshes(t *testing.T) {
	// Zero ExpiresAt means we don't know if it's valid - should try refresh
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"zero-refreshed","token_type":"Bearer","expires_in":3600}`)
	}))
	defer tokenServer.Close()

	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"mcp-notion": {
				Provider: "mcp-notion",
				Token:    "unknown-expiry",
				// ExpiresAt is zero
				Metadata: map[string]string{
					"refresh_token": "my-refresh-token",
				},
			},
		},
	}

	server := &config.MCPServerConfig{
		Name: "notion",
		Auth: &config.MCPAuthConfig{
			Grant:    "mcp-notion",
			Type:     "oauth",
			TokenURL: tokenServer.URL,
			ClientID: "client-id",
		},
	}

	p := &Proxy{credStore: mockStore}
	cred := mockStore.creds["mcp-notion"]
	token := p.resolveOAuthToken(server, cred)

	// Zero ExpiresAt means time.Until(zero) is very negative, so it should refresh
	if token != "zero-refreshed" {
		t.Errorf("resolveOAuthToken() = %q, want %q", token, "zero-refreshed")
	}
}

func TestMCPOAuthInjection_EndToEnd(t *testing.T) {
	// Full end-to-end: expired OAuth token triggers refresh, injects Bearer into request
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"fresh-e2e-token","token_type":"Bearer","expires_in":3600}`)
	}))
	defer tokenServer.Close()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Header.Get("Authorization")))
	}))
	defer backend.Close()

	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"mcp-notion": {
				Provider:  "mcp-notion",
				Token:     "expired-oauth-token",
				ExpiresAt: time.Now().Add(-5 * time.Minute),
				Metadata: map[string]string{
					"refresh_token": "my-refresh-token",
				},
			},
		},
	}

	mcpServers := []config.MCPServerConfig{
		{
			Name: "notion",
			URL:  backend.URL,
			Auth: &config.MCPAuthConfig{
				Grant:    "mcp-notion",
				Type:     "oauth",
				TokenURL: tokenServer.URL,
				ClientID: "client-id",
			},
		},
	}

	p := &Proxy{
		credStore:  mockStore,
		mcpServers: mcpServers,
	}

	req := httptest.NewRequest("GET", backend.URL, nil)
	req.Header.Set("Authorization", "moat-stub-mcp-notion")

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	expected := "Bearer fresh-e2e-token"
	if rec.Body.String() != expected {
		t.Errorf("expected body %q, got %q", expected, rec.Body.String())
	}
}
