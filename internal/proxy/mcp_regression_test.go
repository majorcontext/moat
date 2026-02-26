package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
)

// TestMCPRelay_NilCredentialStore tests that handleMCPRelay fails gracefully
// when credStore is nil and no RunContextData is present.
func TestMCPRelay_NilCredentialStore(t *testing.T) {
	// Create proxy without credential store (nil) and no context resolver.
	// This simulates a misconfigured proxy where neither daemon-mode
	// RunContextData nor a legacy credStore provides credentials.
	p := &Proxy{
		credStore: nil,
		mcpServers: []config.MCPServerConfig{
			{
				Name: "context7",
				URL:  "https://mcp.context7.com/mcp",
				Auth: &config.MCPAuthConfig{
					Grant:  "mcp-context7",
					Header: "API_KEY",
				},
			},
		},
	}

	// Create test request to MCP relay endpoint
	req := httptest.NewRequest("POST", "/mcp/context7/v1/endpoint", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	p.handleMCPRelay(rec, req)

	// Should fail gracefully with 500 and helpful error message
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Failed to load credential") {
		t.Errorf("error message should mention failed credential load, got: %s", body)
	}
}

// TestMCPRelay_DaemonModeCredentials tests that handleMCPRelay resolves
// credentials from RunContextData when credStore is nil (daemon mode).
func TestMCPRelay_DaemonModeCredentials(t *testing.T) {
	// Mock backend that records the received header.
	var receivedKey string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedKey = r.Header.Get("X-Api-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	p := &Proxy{
		credStore: nil, // No store — daemon mode
		mcpServers: []config.MCPServerConfig{
			{
				Name: "test-server",
				URL:  backend.URL,
				Auth: &config.MCPAuthConfig{
					Grant:  "mcp-test",
					Header: "X-Api-Key",
				},
			},
		},
	}

	// Build request with RunContextData carrying the credential and MCP config.
	req := httptest.NewRequest("GET", "/mcp/test-server", nil)
	rc := &RunContextData{
		Credentials: map[string]credentialHeader{
			"example.com": {Name: "X-Api-Key", Value: "real-secret", Grant: "mcp-test"},
		},
		MCPServers: []config.MCPServerConfig{
			{
				Name: "test-server",
				URL:  backend.URL,
				Auth: &config.MCPAuthConfig{
					Grant:  "mcp-test",
					Header: "X-Api-Key",
				},
			},
		},
	}
	ctx := context.WithValue(req.Context(), runContextKey, rc)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	p.handleMCPRelay(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if receivedKey != "real-secret" {
		t.Errorf("backend received X-Api-Key = %q, want %q", receivedKey, "real-secret")
	}
}

// TestMCPRelay_MissingCredential tests that handleMCPRelay provides helpful
// error when credential is not stored.
func TestMCPRelay_MissingCredential(t *testing.T) {
	// Create proxy with empty credential store
	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{},
	}

	p := &Proxy{
		credStore: mockStore,
		mcpServers: []config.MCPServerConfig{
			{
				Name: "context7",
				URL:  "https://mcp.context7.com/mcp",
				Auth: &config.MCPAuthConfig{
					Grant:  "mcp-context7",
					Header: "API_KEY",
				},
			},
		},
	}

	req := httptest.NewRequest("POST", "/mcp/context7", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	p.handleMCPRelay(rec, req)

	// Should fail with helpful error
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Failed to load credential") {
		t.Errorf("error should mention failed to load credential, got: %s", body)
	}
	if !strings.Contains(body, "moat grant mcp context7") {
		t.Errorf("error should suggest grant command, got: %s", body)
	}
}

// TestMCPRelay_PathHandling tests various path edge cases to prevent
// regressions in URL path handling.
func TestMCPRelay_PathHandling(t *testing.T) {
	tests := []struct {
		name             string
		serverPathSuffix string
		requestPath      string
		expectedPath     string
		expectedQuery    string
	}{
		{
			name:             "root path",
			serverPathSuffix: "/api",
			requestPath:      "/mcp/test",
			expectedPath:     "/api",
			expectedQuery:    "",
		},
		{
			name:             "trailing slash on server URL",
			serverPathSuffix: "/api/",
			requestPath:      "/mcp/test",
			expectedPath:     "/api/",
			expectedQuery:    "",
		},
		{
			name:             "nested path",
			serverPathSuffix: "/api",
			requestPath:      "/mcp/test/v1/endpoint",
			expectedPath:     "/api/v1/endpoint",
			expectedQuery:    "",
		},
		{
			name:             "nested path with trailing slash",
			serverPathSuffix: "/api/",
			requestPath:      "/mcp/test/v1/endpoint",
			expectedPath:     "/api/v1/endpoint",
			expectedQuery:    "",
		},
		{
			name:             "query parameters",
			serverPathSuffix: "/api",
			requestPath:      "/mcp/test/v1/endpoint?param=value&other=123",
			expectedPath:     "/api/v1/endpoint",
			expectedQuery:    "param=value&other=123",
		},
		{
			name:             "slash after server name",
			serverPathSuffix: "/api",
			requestPath:      "/mcp/test/",
			expectedPath:     "/api", // "/" is skipped by the handler
			expectedQuery:    "",
		},
		{
			name:             "empty path after server name",
			serverPathSuffix: "/api",
			requestPath:      "/mcp/test",
			expectedPath:     "/api",
			expectedQuery:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Mock backend that records the request path
			var receivedPath, receivedQuery string
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedPath = r.URL.Path
				receivedQuery = r.URL.RawQuery
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("OK"))
			}))
			defer backend.Close()

			mockStore := &mockCredentialStore{
				creds: map[credential.Provider]*credential.Credential{},
			}

			p := &Proxy{
				credStore: mockStore,
				mcpServers: []config.MCPServerConfig{
					{
						Name: "test",
						URL:  backend.URL + tt.serverPathSuffix,
						Auth: nil, // No auth for simplicity
					},
				},
			}

			req := httptest.NewRequest("GET", tt.requestPath, nil)
			rec := httptest.NewRecorder()
			p.handleMCPRelay(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want %d, body: %s", rec.Code, http.StatusOK, rec.Body.String())
			}

			if receivedPath != tt.expectedPath {
				t.Errorf("path = %q, want %q", receivedPath, tt.expectedPath)
			}

			if receivedQuery != tt.expectedQuery {
				t.Errorf("query = %q, want %q", receivedQuery, tt.expectedQuery)
			}
		})
	}
}

// TestMCPRelay_ServerNotFound tests error handling for non-existent MCP servers.
func TestMCPRelay_ServerNotFound(t *testing.T) {
	p := &Proxy{
		credStore:  &mockCredentialStore{creds: map[credential.Provider]*credential.Credential{}},
		mcpServers: []config.MCPServerConfig{},
	}

	req := httptest.NewRequest("POST", "/mcp/nonexistent", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	p.handleMCPRelay(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "not configured") {
		t.Errorf("error should mention server not configured, got: %s", body)
	}
	if !strings.Contains(body, "nonexistent") {
		t.Errorf("error should include server name, got: %s", body)
	}
}

// TestMCPRelay_HeaderInjection verifies that credentials are injected as headers.
func TestMCPRelay_HeaderInjection(t *testing.T) {
	var receivedHeader string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("X-API-Key")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer backend.Close()

	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"mcp-test": {
				Provider: "mcp-test",
				Token:    "secret-token-123",
			},
		},
	}

	p := &Proxy{
		credStore: mockStore,
		mcpServers: []config.MCPServerConfig{
			{
				Name: "test",
				URL:  backend.URL,
				Auth: &config.MCPAuthConfig{
					Grant:  "mcp-test",
					Header: "X-API-Key",
				},
			},
		},
	}

	req := httptest.NewRequest("POST", "/mcp/test/v1/endpoint", strings.NewReader(`{"test":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleMCPRelay(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if receivedHeader != "secret-token-123" {
		t.Errorf("received header = %q, want %q", receivedHeader, "secret-token-123")
	}
}

// TestMCPRelay_SSEStreaming verifies that SSE (Server-Sent Events) responses
// are properly streamed with flushing.
func TestMCPRelay_SSEStreaming(t *testing.T) {
	// Create a backend that sends SSE events
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		// Send SSE events
		w.Write([]byte("data: event1\n\n"))
		w.Write([]byte("data: event2\n\n"))
	}))
	defer backend.Close()

	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{},
	}

	p := &Proxy{
		credStore: mockStore,
		mcpServers: []config.MCPServerConfig{
			{
				Name: "sse-test",
				URL:  backend.URL,
				Auth: nil,
			},
		},
	}

	req := httptest.NewRequest("GET", "/mcp/sse-test", nil)
	rec := httptest.NewRecorder()
	p.handleMCPRelay(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Verify SSE headers are preserved
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream")
	}

	// Verify body contains SSE events
	body := rec.Body.String()
	if !strings.Contains(body, "data: event1") {
		t.Errorf("body should contain event1, got: %s", body)
	}
	if !strings.Contains(body, "data: event2") {
		t.Errorf("body should contain event2, got: %s", body)
	}
}

// TestMCPRelay_RequestBodyPreserved verifies that request bodies are
// properly forwarded to the MCP server.
func TestMCPRelay_RequestBodyPreserved(t *testing.T) {
	var receivedBody string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBody = string(bodyBytes)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{},
	}

	p := &Proxy{
		credStore: mockStore,
		mcpServers: []config.MCPServerConfig{
			{
				Name: "test",
				URL:  backend.URL,
				Auth: nil,
			},
		},
	}

	requestBody := `{"method":"test","params":{"key":"value"}}`
	req := httptest.NewRequest("POST", "/mcp/test", strings.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleMCPRelay(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	if receivedBody != requestBody {
		t.Errorf("body = %q, want %q", receivedBody, requestBody)
	}
}

// TestMCPRelay_ProxyHeadersFiltered verifies that proxy-specific headers
// are not forwarded to the MCP server.
func TestMCPRelay_ProxyHeadersFiltered(t *testing.T) {
	var receivedHeaders http.Header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{},
	}

	p := &Proxy{
		credStore: mockStore,
		mcpServers: []config.MCPServerConfig{
			{
				Name: "test",
				URL:  backend.URL,
				Auth: nil,
			},
		},
	}

	req := httptest.NewRequest("POST", "/mcp/test", strings.NewReader("{}"))
	req.Header.Set("Proxy-Authorization", "Basic secret")
	req.Header.Set("Proxy-Connection", "keep-alive")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Custom-Header", "custom-value")

	rec := httptest.NewRecorder()
	p.handleMCPRelay(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Proxy headers should be filtered out
	if receivedHeaders.Get("Proxy-Authorization") != "" {
		t.Error("Proxy-Authorization should be filtered out")
	}
	if receivedHeaders.Get("Proxy-Connection") != "" {
		t.Error("Proxy-Connection should be filtered out")
	}

	// Other headers should be preserved
	if receivedHeaders.Get("Content-Type") != "application/json" {
		t.Error("Content-Type should be preserved")
	}
	if receivedHeaders.Get("X-Custom-Header") != "custom-value" {
		t.Error("X-Custom-Header should be preserved")
	}
}

// TestMCPRelay_InvalidServerURL tests error handling for malformed MCP server URLs.
func TestMCPRelay_InvalidServerURL(t *testing.T) {
	p := &Proxy{
		credStore: &mockCredentialStore{creds: map[credential.Provider]*credential.Credential{}},
		mcpServers: []config.MCPServerConfig{
			{
				Name: "bad",
				URL:  "://invalid-url",
				Auth: nil,
			},
		},
	}

	req := httptest.NewRequest("POST", "/mcp/bad", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	p.handleMCPRelay(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Invalid MCP server URL") {
		t.Errorf("error should mention invalid URL, got: %s", body)
	}
}

// TestMCPRelay_NoAuth tests MCP servers without authentication.
func TestMCPRelay_NoAuth(t *testing.T) {
	var receivedAuthHeader string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer backend.Close()

	p := &Proxy{
		credStore: &mockCredentialStore{creds: map[credential.Provider]*credential.Credential{}},
		mcpServers: []config.MCPServerConfig{
			{
				Name: "public",
				URL:  backend.URL,
				Auth: nil, // No authentication
			},
		},
	}

	req := httptest.NewRequest("POST", "/mcp/public", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	p.handleMCPRelay(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Should not inject any auth header
	if receivedAuthHeader != "" {
		t.Errorf("auth header should be empty, got: %q", receivedAuthHeader)
	}
}
