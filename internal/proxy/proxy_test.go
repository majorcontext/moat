package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestProxy_ForwardsRequests(t *testing.T) {
	// Create a test backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("backend response"))
	}))
	defer backend.Close()

	// Create proxy
	p := NewProxy()

	// Create proxy server
	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	// Make request through proxy
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustParseURL(proxyServer.URL)),
		},
	}

	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "backend response" {
		t.Errorf("body = %q, want %q", string(body), "backend response")
	}
}

func TestProxy_InjectsAuthHeader(t *testing.T) {
	var receivedAuth string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	p := NewProxy()
	p.SetCredential("127.0.0.1", "Bearer test-token")

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustParseURL(proxyServer.URL)),
		},
	}

	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()

	if receivedAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", receivedAuth, "Bearer test-token")
	}
}

func TestProxy_AuthTokenRequired(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("backend response"))
	}))
	defer backend.Close()

	p := NewProxy()
	p.SetAuthToken("secret-token-123")

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	// Request without auth should fail
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustParseURL(proxyServer.URL)),
		},
	}

	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Errorf("status = %d, want %d (Proxy Auth Required)", resp.StatusCode, http.StatusProxyAuthRequired)
	}
}

func TestProxy_AuthTokenValidBasicAuth(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("backend response"))
	}))
	defer backend.Close()

	p := NewProxy()
	p.SetAuthToken("secret-token-123")

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	// Request with valid Basic auth (username:token format) should succeed
	proxyURL := mustParseURL(proxyServer.URL)
	proxyURL.User = url.UserPassword("moat", "secret-token-123")

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}

	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "backend response" {
		t.Errorf("body = %q, want %q", string(body), "backend response")
	}
}

func TestProxy_AuthTokenInvalidToken(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("backend response"))
	}))
	defer backend.Close()

	p := NewProxy()
	p.SetAuthToken("secret-token-123")

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	// Request with wrong token should fail
	proxyURL := mustParseURL(proxyServer.URL)
	proxyURL.User = url.UserPassword("moat", "wrong-token")

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}

	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Errorf("status = %d, want %d (Proxy Auth Required)", resp.StatusCode, http.StatusProxyAuthRequired)
	}
}

func TestProxy_NetworkPolicyPermissive(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("backend response"))
	}))
	defer backend.Close()

	p := NewProxy()
	p.SetNetworkPolicy("permissive", []string{}, []string{})

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustParseURL(proxyServer.URL)),
		},
	}

	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestProxy_NetworkPolicyStrictBlocked(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("backend response"))
	}))
	defer backend.Close()

	p := NewProxy()
	// Set strict policy with no allowed hosts
	p.SetNetworkPolicy("strict", []string{}, []string{})

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustParseURL(proxyServer.URL)),
		},
	}

	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusProxyAuthRequired)
	}

	if resp.Header.Get("X-Moat-Blocked") != "network-policy" {
		t.Errorf("X-Moat-Blocked header missing or wrong")
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "blocked by network policy") {
		t.Errorf("response body should mention network policy blocking")
	}
}

func TestProxy_NetworkPolicyStrictAllowed(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("backend response"))
	}))
	defer backend.Close()

	// Extract port from backend URL to allow it
	backendURL := mustParseURL(backend.URL)
	allowPattern := "127.0.0.1:" + backendURL.Port()

	p := NewProxy()
	// Allow localhost/127.0.0.1 with the specific port
	p.SetNetworkPolicy("strict", []string{allowPattern}, []string{})

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustParseURL(proxyServer.URL)),
		},
	}

	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "backend response" {
		t.Errorf("body = %q, want %q", string(body), "backend response")
	}
}

func TestProxy_NetworkPolicyWithGrants(t *testing.T) {
	p := NewProxy()
	// Set policy with github grant
	p.SetNetworkPolicy("strict", []string{}, []string{"github"})

	// Should allow github.com (port 443)
	if !p.checkNetworkPolicy("github.com", 443) {
		t.Errorf("github.com:443 should be allowed with github grant")
	}

	// Should allow api.github.com (port 443)
	if !p.checkNetworkPolicy("api.github.com", 443) {
		t.Errorf("api.github.com:443 should be allowed with github grant")
	}

	// Should allow wildcard match for githubusercontent.com
	if !p.checkNetworkPolicy("raw.githubusercontent.com", 443) {
		t.Errorf("raw.githubusercontent.com:443 should be allowed with github grant (wildcard)")
	}

	// Should block non-github hosts
	if p.checkNetworkPolicy("example.com", 443) {
		t.Errorf("example.com:443 should be blocked")
	}
}

func TestProxy_NetworkPolicyMixedAllowsAndGrants(t *testing.T) {
	p := NewProxy()
	// Combine explicit allows and grants
	p.SetNetworkPolicy("strict", []string{"api.example.com"}, []string{"github"})

	// Should allow explicit pattern
	if !p.checkNetworkPolicy("api.example.com", 443) {
		t.Errorf("api.example.com:443 should be allowed (explicit)")
	}

	// Should allow github from grant
	if !p.checkNetworkPolicy("github.com", 443) {
		t.Errorf("github.com:443 should be allowed (grant)")
	}

	// Should block others
	if p.checkNetworkPolicy("evil.com", 443) {
		t.Errorf("evil.com:443 should be blocked")
	}
}

func TestProxy_NetworkPolicyLogging(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("backend response"))
	}))
	defer backend.Close()

	p := NewProxy()
	p.SetNetworkPolicy("strict", []string{}, []string{}) // Block everything

	var loggedMethod string
	var loggedStatus int

	p.SetLogger(func(data RequestLogData) {
		loggedMethod = data.Method
		loggedStatus = data.StatusCode
	})

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustParseURL(proxyServer.URL)),
		},
	}

	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()

	if loggedStatus != http.StatusProxyAuthRequired {
		t.Errorf("logged status = %d, want %d", loggedStatus, http.StatusProxyAuthRequired)
	}

	if loggedMethod != "GET" {
		t.Errorf("logged method = %q, want GET", loggedMethod)
	}
}

func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// TestProxy_SetCredentialHeader tests custom header injection (e.g., x-api-key for Anthropic).
func TestProxy_SetCredentialHeader(t *testing.T) {
	var receivedHeader string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("x-api-key")
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	p := NewProxy()
	p.SetCredentialHeader("127.0.0.1", "x-api-key", "sk-ant-test-key")

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustParseURL(proxyServer.URL)),
		},
	}

	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()

	if receivedHeader != "sk-ant-test-key" {
		t.Errorf("x-api-key = %q, want %q", receivedHeader, "sk-ant-test-key")
	}
}

// TestProxy_SetCredential_UsesAuthorizationHeader verifies SetCredential uses Authorization header.
func TestProxy_SetCredential_UsesAuthorizationHeader(t *testing.T) {
	p := NewProxy()
	p.SetCredential("api.example.com", "Bearer token123")

	cred, ok := p.getCredential("api.example.com")
	if !ok {
		t.Fatal("expected credential to be set")
	}
	if cred.Name != "Authorization" {
		t.Errorf("header name = %q, want %q", cred.Name, "Authorization")
	}
	if cred.Value != "Bearer token123" {
		t.Errorf("header value = %q, want %q", cred.Value, "Bearer token123")
	}
}

// TestProxy_ExtraHeaders_MergesWithExisting verifies that extra headers are
// merged with client-sent headers rather than replacing them.
func TestProxy_ExtraHeaders_MergesWithExisting(t *testing.T) {
	var receivedBeta string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBeta = r.Header.Get("anthropic-beta")
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	p := NewProxy()
	p.AddExtraHeader("127.0.0.1", "anthropic-beta", "oauth-2025-04-20")

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustParseURL(proxyServer.URL)),
		},
	}

	req, _ := http.NewRequest("GET", backend.URL, nil)
	req.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()

	want := "prompt-caching-2024-07-31,oauth-2025-04-20"
	if receivedBeta != want {
		t.Errorf("anthropic-beta = %q, want %q", receivedBeta, want)
	}
}

// TestProxy_ExtraHeaders_SetsWhenAbsent verifies that extra headers are set
// when the client doesn't send them.
func TestProxy_ExtraHeaders_SetsWhenAbsent(t *testing.T) {
	var receivedBeta string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBeta = r.Header.Get("anthropic-beta")
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	p := NewProxy()
	p.AddExtraHeader("127.0.0.1", "anthropic-beta", "oauth-2025-04-20")

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustParseURL(proxyServer.URL)),
		},
	}

	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()

	if receivedBeta != "oauth-2025-04-20" {
		t.Errorf("anthropic-beta = %q, want %q", receivedBeta, "oauth-2025-04-20")
	}
}

// TestProxy_RemoveRequestHeader verifies that client-sent headers can be
// stripped before forwarding.
func TestProxy_RemoveRequestHeader(t *testing.T) {
	var receivedAPIKey string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAPIKey = r.Header.Get("x-api-key")
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	p := NewProxy()
	p.SetCredential("127.0.0.1", "Bearer real-token")
	p.RemoveRequestHeader("127.0.0.1", "x-api-key")

	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustParseURL(proxyServer.URL)),
		},
	}

	req, _ := http.NewRequest("GET", backend.URL, nil)
	req.Header.Set("x-api-key", "stale-placeholder")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()

	if receivedAPIKey != "" {
		t.Errorf("x-api-key should be stripped, got %q", receivedAPIKey)
	}
}

// TestIsTextContentType verifies content type detection for body capture.
func TestIsTextContentType(t *testing.T) {
	tests := []struct {
		contentType string
		want        bool
	}{
		{"text/plain", true},
		{"text/html", true},
		{"application/json", true},
		{"application/xml", true},
		{"application/x-www-form-urlencoded", true},
		{"text/javascript", true},
		{"application/javascript", true},
		{"image/png", false},
		{"image/jpeg", false},
		{"application/octet-stream", false},
		{"application/pdf", false},
		{"video/mp4", false},
		{"", false},
		{"TEXT/PLAIN", true},       // case insensitive
		{"Application/JSON", true}, // case insensitive
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			got := isTextContentType(tt.contentType)
			if got != tt.want {
				t.Errorf("isTextContentType(%q) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}

// TestCaptureBody_TruncatesLargeBody verifies body capture truncation at MaxBodySize
// while still forwarding the full body.
func TestCaptureBody_TruncatesLargeBody(t *testing.T) {
	// Create a body larger than MaxBodySize (8KB)
	largeBody := strings.Repeat("x", MaxBodySize+1000)
	body := io.NopCloser(strings.NewReader(largeBody))

	captured, newBody := captureBody(body, "application/json")

	// Captured portion should be truncated to MaxBodySize
	if len(captured) != MaxBodySize {
		t.Errorf("captured length = %d, want %d", len(captured), MaxBodySize)
	}

	// Full body should still be readable and contain ALL original data
	fullData, err := io.ReadAll(newBody)
	if err != nil {
		t.Fatalf("reading new body: %v", err)
	}
	if len(fullData) != len(largeBody) {
		t.Errorf("full body length = %d, want %d", len(fullData), len(largeBody))
	}
}

// TestCaptureBody_StreamsVeryLargeBody verifies bodies much larger than MaxBodySize
// are fully forwarded (not truncated).
func TestCaptureBody_StreamsVeryLargeBody(t *testing.T) {
	// Create a body much larger than MaxBodySize (e.g., 100KB)
	veryLargeBody := strings.Repeat("y", 100*1024)
	body := io.NopCloser(strings.NewReader(veryLargeBody))

	captured, newBody := captureBody(body, "application/json")

	// Captured should still be truncated to MaxBodySize
	if len(captured) != MaxBodySize {
		t.Errorf("captured length = %d, want %d", len(captured), MaxBodySize)
	}

	// But full body must be fully forwarded
	fullData, err := io.ReadAll(newBody)
	if err != nil {
		t.Fatalf("reading new body: %v", err)
	}
	if len(fullData) != len(veryLargeBody) {
		t.Errorf("full body length = %d, want %d (body was truncated!)", len(fullData), len(veryLargeBody))
	}

	// Close should work
	if err := newBody.Close(); err != nil {
		t.Errorf("close error: %v", err)
	}
}

// TestCaptureBody_SkipsBinaryContent verifies binary content types are not captured.
func TestCaptureBody_SkipsBinaryContent(t *testing.T) {
	originalData := []byte{0x89, 0x50, 0x4E, 0x47} // PNG magic bytes
	body := io.NopCloser(bytes.NewReader(originalData))

	captured, newBody := captureBody(body, "image/png")

	// Should not capture binary content
	if captured != nil {
		t.Errorf("captured = %v, want nil for binary content", captured)
	}

	// Body should be returned unchanged
	data, err := io.ReadAll(newBody)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if !bytes.Equal(data, originalData) {
		t.Errorf("body data changed for binary content")
	}
}

// TestCaptureBody_NilBody verifies nil body handling.
func TestCaptureBody_NilBody(t *testing.T) {
	captured, newBody := captureBody(nil, "application/json")

	if captured != nil {
		t.Errorf("captured = %v, want nil", captured)
	}
	if newBody != nil {
		t.Errorf("newBody = %v, want nil", newBody)
	}
}

// TestFilterHeaders_RedactsInjectedCredential verifies credential redaction.
func TestFilterHeaders_RedactsInjectedCredential(t *testing.T) {
	headers := http.Header{
		"Authorization": []string{"Bearer secret-token"},
		"Content-Type":  []string{"application/json"},
		"Accept":        []string{"*/*"},
	}

	// When auth was injected, Authorization should be redacted
	filtered := FilterHeaders(headers, true, "Authorization")

	if filtered["Authorization"] != "[REDACTED]" {
		t.Errorf("Authorization = %q, want %q", filtered["Authorization"], "[REDACTED]")
	}
	if filtered["Content-Type"] != "application/json" {
		t.Errorf("Content-Type = %q, want %q", filtered["Content-Type"], "application/json")
	}
}

// TestFilterHeaders_PreservesNonInjectedCredential verifies non-injected headers are preserved.
func TestFilterHeaders_PreservesNonInjectedCredential(t *testing.T) {
	headers := http.Header{
		"Authorization": []string{"Bearer user-token"},
		"Content-Type":  []string{"application/json"},
	}

	// When auth was NOT injected, Authorization should be preserved
	filtered := FilterHeaders(headers, false, "")

	if filtered["Authorization"] != "Bearer user-token" {
		t.Errorf("Authorization = %q, want %q", filtered["Authorization"], "Bearer user-token")
	}
}

// TestFilterHeaders_RedactsCustomHeader verifies custom header redaction (like x-api-key).
func TestFilterHeaders_RedactsCustomHeader(t *testing.T) {
	headers := http.Header{
		"X-Api-Key":    []string{"sk-ant-secret"},
		"Content-Type": []string{"application/json"},
	}

	// When custom header was injected, it should be redacted
	filtered := FilterHeaders(headers, true, "X-Api-Key")

	if filtered["X-Api-Key"] != "[REDACTED]" {
		t.Errorf("X-Api-Key = %q, want %q", filtered["X-Api-Key"], "[REDACTED]")
	}
}

// TestFilterHeaders_FiltersProxyHeaders verifies proxy headers are always filtered out.
func TestFilterHeaders_FiltersProxyHeaders(t *testing.T) {
	headers := http.Header{
		"Proxy-Authorization": []string{"Basic secret"},
		"Proxy-Connection":    []string{"keep-alive"},
		"Content-Type":        []string{"application/json"},
	}

	filtered := FilterHeaders(headers, false, "")

	if _, exists := filtered["Proxy-Authorization"]; exists {
		t.Error("Proxy-Authorization should be filtered out")
	}
	if _, exists := filtered["Proxy-Connection"]; exists {
		t.Error("Proxy-Connection should be filtered out")
	}
	if filtered["Content-Type"] != "application/json" {
		t.Errorf("Content-Type = %q, want %q", filtered["Content-Type"], "application/json")
	}
}

// TestFilterHeaders_JoinsMultipleValues verifies multi-value headers are joined.
func TestFilterHeaders_JoinsMultipleValues(t *testing.T) {
	headers := http.Header{
		"Accept": []string{"text/html", "application/json", "*/*"},
	}

	filtered := FilterHeaders(headers, false, "")

	expected := "text/html, application/json, */*"
	if filtered["Accept"] != expected {
		t.Errorf("Accept = %q, want %q", filtered["Accept"], expected)
	}
}

// TestFilterHeaders_NilHeaders verifies nil header handling.
func TestFilterHeaders_NilHeaders(t *testing.T) {
	filtered := FilterHeaders(nil, false, "")

	if filtered != nil {
		t.Errorf("filtered = %v, want nil", filtered)
	}
}
