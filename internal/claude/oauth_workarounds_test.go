package claude

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/credential"
)

func TestOAuthEndpointWorkarounds(t *testing.T) {
	// Verify the list contains expected endpoints
	expectedEndpoints := []string{
		"/api/oauth/profile",
		"/api/oauth/usage",
	}

	if len(OAuthEndpointWorkarounds) != len(expectedEndpoints) {
		t.Errorf("OAuthEndpointWorkarounds has %d endpoints, expected %d", len(OAuthEndpointWorkarounds), len(expectedEndpoints))
	}

	for _, expected := range expectedEndpoints {
		found := false
		for _, actual := range OAuthEndpointWorkarounds {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected endpoint %q not found in OAuthEndpointWorkarounds", expected)
		}
	}
}

func TestCreateOAuthEndpointTransformer_Transform403PermissionError(t *testing.T) {
	transformer := CreateOAuthEndpointTransformer()

	tests := []struct {
		name            string
		path            string
		statusCode      int
		body            string
		shouldTransform bool
		expectedBody    string
	}{
		{
			name:            "transforms 403 permission_error on /api/oauth/profile",
			path:            "/api/oauth/profile",
			statusCode:      403,
			body:            `{"type":"error","error":{"type":"permission_error","message":"OAuth token does not meet scope requirement user:profile"}}`,
			shouldTransform: true,
			expectedBody:    `{"id":"","email":"","name":""}`,
		},
		{
			name:            "transforms 403 permission_error on /api/oauth/usage",
			path:            "/api/oauth/usage",
			statusCode:      403,
			body:            `{"type":"error","error":{"type":"permission_error","message":"OAuth token does not meet scope requirement user:profile"}}`,
			shouldTransform: true,
			expectedBody:    `{"usage":{}}`,
		},
		{
			name:            "transforms all 403s on oauth endpoints (simplified - no body check)",
			path:            "/api/oauth/profile",
			statusCode:      403,
			body:            `{"type":"error","error":{"type":"rate_limit_error","message":"Rate limit exceeded"}}`,
			shouldTransform: true,
			expectedBody:    `{"id":"","email":"","name":""}`,
		},
		{
			name:            "does not transform 404 on oauth endpoint",
			path:            "/api/oauth/profile",
			statusCode:      404,
			body:            `{"error":"not found"}`,
			shouldTransform: false,
		},
		{
			name:            "does not transform 403 on non-workaround endpoint",
			path:            "/api/oauth/other",
			statusCode:      403,
			body:            `{"type":"error","error":{"type":"permission_error"}}`,
			shouldTransform: false,
		},
		{
			name:            "does not transform 200 success",
			path:            "/api/oauth/profile",
			statusCode:      200,
			body:            `{"id":"123","email":"user@example.com"}`,
			shouldTransform: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &http.Request{
				URL: &url.URL{Path: tt.path},
			}

			resp := &http.Response{
				StatusCode: tt.statusCode,
				Body:       io.NopCloser(strings.NewReader(tt.body)),
				Header:     http.Header{},
			}

			resultInterface, transformed := transformer(req, resp)

			if transformed != tt.shouldTransform {
				t.Errorf("transformed = %v, want %v", transformed, tt.shouldTransform)
			}

			if transformed {
				result, ok := resultInterface.(*http.Response)
				if !ok {
					t.Fatal("transformed response is not *http.Response")
				}

				// Check status code
				if result.StatusCode != http.StatusOK {
					t.Errorf("transformed status = %d, want %d", result.StatusCode, http.StatusOK)
				}

				// Check X-Moat-Transformed header
				if result.Header.Get("X-Moat-Transformed") != "oauth-scope-workaround" {
					t.Errorf("X-Moat-Transformed header = %q, want %q",
						result.Header.Get("X-Moat-Transformed"), "oauth-scope-workaround")
				}

				// Check body
				bodyBytes, err := io.ReadAll(result.Body)
				if err != nil {
					t.Fatalf("failed to read transformed body: %v", err)
				}
				if string(bodyBytes) != tt.expectedBody {
					t.Errorf("transformed body = %q, want %q", string(bodyBytes), tt.expectedBody)
				}

				// Check Content-Type
				if result.Header.Get("Content-Type") != "application/json" {
					t.Errorf("Content-Type = %q, want %q", result.Header.Get("Content-Type"), "application/json")
				}
			} else {
				// Non-transformed response should be returned as-is
				result, ok := resultInterface.(*http.Response)
				if !ok {
					t.Fatal("non-transformed response is not *http.Response")
				}

				if result.StatusCode != tt.statusCode {
					t.Errorf("status code changed from %d to %d", tt.statusCode, result.StatusCode)
				}
			}
		})
	}
}

func TestCreateOAuthEndpointTransformer_HandleReadError(t *testing.T) {
	transformer := CreateOAuthEndpointTransformer()

	req := &http.Request{
		URL: &url.URL{Path: "/api/oauth/profile"},
	}

	// Create a response with a body that errors on read
	resp := &http.Response{
		StatusCode: 403,
		Body:       &errorReader{},
		Header:     http.Header{},
	}

	result, transformed := transformer(req, resp)

	// With simplified logic (no body reading), this should still transform
	// because we only check status + path, not body content
	if !transformed {
		t.Error("should transform 403 on oauth endpoint regardless of body read errors")
	}

	// Should return transformed response
	newResp, ok := result.(*http.Response)
	if !ok {
		t.Error("result should be *http.Response")
	}
	if newResp.StatusCode != http.StatusOK {
		t.Errorf("status should be 200, got %d", newResp.StatusCode)
	}
}

func TestConfigureProxy_RegistersTransformerForOAuth(t *testing.T) {
	setup := &AnthropicSetup{}
	mockProxy := &mockProxyConfigurer{
		credentials:  make(map[string]string),
		extraHeaders: make(map[string]map[string]string),
	}

	// OAuth token should register transformer
	cred := &credential.Credential{Token: "sk-ant-oat01-abc123"}
	setup.ConfigureProxy(mockProxy, cred)

	if len(mockProxy.transformers["api.anthropic.com"]) != 1 {
		t.Errorf("expected 1 transformer registered, got %d", len(mockProxy.transformers["api.anthropic.com"]))
	}
}

func TestConfigureProxy_NoTransformerForAPIKey(t *testing.T) {
	setup := &AnthropicSetup{}
	mockProxy := &mockProxyConfigurer{
		credentials:  make(map[string]string),
		extraHeaders: make(map[string]map[string]string),
	}

	// API key should NOT register transformer
	cred := &credential.Credential{Token: "sk-ant-api01-abc123"}
	setup.ConfigureProxy(mockProxy, cred)

	if len(mockProxy.transformers["api.anthropic.com"]) != 0 {
		t.Errorf("expected 0 transformers for API key, got %d", len(mockProxy.transformers["api.anthropic.com"]))
	}
}

// errorReader always returns an error when read.
type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, io.ErrUnexpectedEOF
}

func (e *errorReader) Close() error {
	return nil
}

func TestCreateEmptyOAuthResponse(t *testing.T) {
	tests := []struct {
		path         string
		expectedBody string
	}{
		{"/api/oauth/profile", `{"id":"","email":"","name":""}`},
		{"/api/oauth/usage", `{"usage":{}}`},
		{"/api/oauth/unknown", `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			resp := createEmptyOAuthResponse(tt.path)

			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
			}

			bodyBytes, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("failed to read body: %v", err)
			}

			if string(bodyBytes) != tt.expectedBody {
				t.Errorf("body = %q, want %q", string(bodyBytes), tt.expectedBody)
			}

			if resp.Header.Get("Content-Type") != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", resp.Header.Get("Content-Type"))
			}

			if resp.Header.Get("X-Moat-Transformed") != "oauth-scope-workaround" {
				t.Errorf("X-Moat-Transformed = %q, want oauth-scope-workaround", resp.Header.Get("X-Moat-Transformed"))
			}
		})
	}
}
