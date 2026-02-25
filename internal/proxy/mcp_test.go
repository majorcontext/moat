package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

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

func TestMCPRelay_HostLocal(t *testing.T) {
	// Host-local MCP server (no auth) should relay requests directly
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tools":["read","write"]}`))
	}))
	defer backend.Close()

	mcpServers := []config.MCPServerConfig{
		{
			Name: "local-tools",
			URL:  backend.URL, // http://127.0.0.1:PORT
			Auth: nil,         // No auth for host-local server
		},
	}

	p := &Proxy{
		mcpServers: mcpServers,
	}

	// Request to /mcp/local-tools should be relayed to backend
	req := httptest.NewRequest("POST", "/mcp/local-tools", nil)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	p.handleMCPRelay(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.String() != `{"tools":["read","write"]}` {
		t.Errorf("expected backend response, got %q", rec.Body.String())
	}
}

func TestMCPRelay_HostLocalWithPath(t *testing.T) {
	// Host-local MCP server should preserve path after server name
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("path:" + r.URL.Path))
	}))
	defer backend.Close()

	mcpServers := []config.MCPServerConfig{
		{
			Name: "local-server",
			URL:  backend.URL + "/mcp",
			Auth: nil,
		},
	}

	p := &Proxy{
		mcpServers: mcpServers,
	}

	req := httptest.NewRequest("POST", "/mcp/local-server/v1/tools", nil)
	rec := httptest.NewRecorder()
	p.handleMCPRelay(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	expected := "path:/mcp/v1/tools"
	if rec.Body.String() != expected {
		t.Errorf("expected %q, got %q", expected, rec.Body.String())
	}
}

func TestMCPRelay_HostLocalWithAuth(t *testing.T) {
	// Host-local MCP server with auth should inject credentials
	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"local-api-key": {
				Provider: "local-api-key",
				Token:    "secret-local-token",
			},
		},
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Header.Get("X-API-Key")))
	}))
	defer backend.Close()

	mcpServers := []config.MCPServerConfig{
		{
			Name: "local-auth",
			URL:  backend.URL,
			Auth: &config.MCPAuthConfig{
				Grant:  "local-api-key",
				Header: "X-API-Key",
			},
		},
	}

	p := &Proxy{
		credStore:  mockStore,
		mcpServers: mcpServers,
	}

	req := httptest.NewRequest("POST", "/mcp/local-auth", nil)
	rec := httptest.NewRecorder()
	p.handleMCPRelay(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.String() != "secret-local-token" {
		t.Errorf("expected 'secret-local-token', got %q", rec.Body.String())
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
