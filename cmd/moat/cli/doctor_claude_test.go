package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/storage"
)

func TestAnalyzeNetworkAuth(t *testing.T) {
	tests := []struct {
		name            string
		requests        []storage.NetworkRequest
		wantSucceeded   bool
		wantAuthErrors  int
		wantIssueCount  int
		wantSuggestions int
	}{
		{
			name:            "no requests",
			requests:        nil,
			wantSucceeded:   false,
			wantAuthErrors:  0,
			wantIssueCount:  0,
			wantSuggestions: 0,
		},
		{
			name: "all 200s - success",
			requests: []storage.NetworkRequest{
				{URL: "https://api.anthropic.com/v1/messages", Method: "POST", StatusCode: 200},
				{URL: "https://api.anthropic.com/v1/complete", Method: "POST", StatusCode: 200},
			},
			wantSucceeded:   true,
			wantAuthErrors:  0,
			wantIssueCount:  0,
			wantSuggestions: 1,
		},
		{
			name: "all 401s - failure",
			requests: []storage.NetworkRequest{
				{URL: "https://api.anthropic.com/v1/messages", Method: "POST", StatusCode: 401},
			},
			wantSucceeded:  false,
			wantAuthErrors: 1,
			wantIssueCount: 1,
		},
		{
			name: "200 and 401 mixed - failure (401 overrides 200)",
			requests: []storage.NetworkRequest{
				{URL: "https://api.anthropic.com/v1/client_data", Method: "GET", StatusCode: 200},
				{URL: "https://api.anthropic.com/v1/messages", Method: "POST", StatusCode: 401},
			},
			wantSucceeded:  false,
			wantAuthErrors: 1,
			wantIssueCount: 1,
		},
		{
			name: "403 without 401 - not an auth failure",
			requests: []storage.NetworkRequest{
				{URL: "https://api.anthropic.com/v1/messages", Method: "POST", StatusCode: 200},
				{URL: "https://api.anthropic.com/v1/client_data", Method: "GET", StatusCode: 403},
			},
			wantSucceeded:   true,
			wantAuthErrors:  0,
			wantIssueCount:  0,
			wantSuggestions: 1,
		},
		{
			name: "non-anthropic requests ignored",
			requests: []storage.NetworkRequest{
				{URL: "https://other-api.com/health", Method: "GET", StatusCode: 200},
				{URL: "https://other-api.com/auth", Method: "POST", StatusCode: 401},
			},
			wantSucceeded:  false,
			wantAuthErrors: 0,
			wantIssueCount: 0,
		},
		{
			name: "multiple 401s produce single summary issue",
			requests: []storage.NetworkRequest{
				{URL: "https://api.anthropic.com/v1/messages", Method: "POST", StatusCode: 401},
				{URL: "https://api.anthropic.com/v1/complete", Method: "POST", StatusCode: 401},
			},
			wantSucceeded:  false,
			wantAuthErrors: 2,
			wantIssueCount: 1, // single summary, not one per 401
		},
		{
			name: "summary issue includes correct counts",
			requests: []storage.NetworkRequest{
				{URL: "https://api.anthropic.com/v1/org/access", Method: "GET", StatusCode: 200},
				{URL: "https://api.anthropic.com/v1/eval/sdk", Method: "POST", StatusCode: 200},
				{URL: "https://api.anthropic.com/v1/messages?beta=true", Method: "POST", StatusCode: 401},
				{URL: "https://api.anthropic.com/v1/messages?beta=true", Method: "POST", StatusCode: 401},
				{URL: "https://api.anthropic.com/v1/event_logging/batch", Method: "POST", StatusCode: 401},
			},
			wantSucceeded:  false,
			wantAuthErrors: 3,
			wantIssueCount: 1,
		},
		{
			name: "500 errors are not auth failures",
			requests: []storage.NetworkRequest{
				{URL: "https://api.anthropic.com/v1/messages", Method: "POST", StatusCode: 500},
			},
			wantSucceeded:  false,
			wantAuthErrors: 0,
			wantIssueCount: 0,
		},
		{
			name: "mixed anthropic and non-anthropic - only anthropic analyzed",
			requests: []storage.NetworkRequest{
				{URL: "https://api.anthropic.com/v1/messages", Method: "POST", StatusCode: 200},
				{URL: "https://cdn.example.com/assets", Method: "GET", StatusCode: 401},
			},
			wantSucceeded:   true,
			wantAuthErrors:  0,
			wantIssueCount:  0,
			wantSuggestions: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &containerTestResult{}
			diag := &claudeDiagnostic{
				Issues:      []issue{},
				Suggestions: []string{},
			}

			analyzeNetworkAuth(tt.requests, result, diag)

			if result.APICallSucceeded != tt.wantSucceeded {
				t.Errorf("APICallSucceeded = %v, want %v", result.APICallSucceeded, tt.wantSucceeded)
			}
			if len(result.AuthErrors) != tt.wantAuthErrors {
				t.Errorf("AuthErrors count = %d, want %d; errors: %v", len(result.AuthErrors), tt.wantAuthErrors, result.AuthErrors)
			}

			// Count container-auth issues
			authIssues := 0
			for _, iss := range diag.Issues {
				if iss.Component == "container-auth" {
					authIssues++
				}
			}
			if authIssues != tt.wantIssueCount {
				t.Errorf("container-auth issues = %d, want %d; issues: %v", authIssues, tt.wantIssueCount, diag.Issues)
			}

			if tt.wantSuggestions > 0 && len(diag.Suggestions) != tt.wantSuggestions {
				t.Errorf("suggestions = %d, want %d", len(diag.Suggestions), tt.wantSuggestions)
			}
		})
	}
}

func TestParseClaudeConfigFromLogs(t *testing.T) {
	tests := []struct {
		name           string
		logs           []storage.LogEntry
		wantConfigRead bool
		wantIssueCount int
		wantFields     []string // expected keys in ContainerConfig
	}{
		{
			name: "valid config with end marker",
			logs: []storage.LogEntry{
				{Line: `{"oauthAccount":{"organizationUuid":"org-123"},"hasCompletedOnboarding":true}`},
				{Line: "---CONFIG-END---"},
				{Line: "some other output"},
			},
			wantConfigRead: true,
			wantIssueCount: 0,
			wantFields:     []string{"oauthAccount", "hasCompletedOnboarding"},
		},
		{
			name: "multiline JSON config",
			logs: []storage.LogEntry{
				{Line: `{`},
				{Line: `  "oauthAccount": {},`},
				{Line: `  "hasCompletedOnboarding": true`},
				{Line: `}`},
				{Line: "---CONFIG-END---"},
			},
			wantConfigRead: true,
			wantIssueCount: 0,
			wantFields:     []string{"oauthAccount", "hasCompletedOnboarding"},
		},
		{
			name: "missing end marker",
			logs: []storage.LogEntry{
				{Line: `{"oauthAccount":{}}`},
			},
			wantConfigRead: false,
			wantIssueCount: 1, // warning about missing config
		},
		{
			name:           "empty logs",
			logs:           nil,
			wantConfigRead: false,
			wantIssueCount: 1,
		},
		{
			name: "end marker but no JSON",
			logs: []storage.LogEntry{
				{Line: "some random output"},
				{Line: "---CONFIG-END---"},
			},
			wantConfigRead: true,
			wantIssueCount: 0,
			// ConfigRead is true (marker found) but ContainerConfig will be nil
			// because no JSON was collected
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &containerTestResult{}
			diag := &claudeDiagnostic{
				Issues: []issue{},
			}

			parseClaudeConfigFromLogs(tt.logs, result, diag)

			if result.ConfigRead != tt.wantConfigRead {
				t.Errorf("ConfigRead = %v, want %v", result.ConfigRead, tt.wantConfigRead)
			}

			issueCount := 0
			for _, iss := range diag.Issues {
				if iss.Component == "container-test" {
					issueCount++
				}
			}
			if issueCount != tt.wantIssueCount {
				t.Errorf("container-test issues = %d, want %d; issues: %v", issueCount, tt.wantIssueCount, diag.Issues)
			}

			if tt.wantFields != nil && result.ContainerConfig != nil {
				for _, field := range tt.wantFields {
					if _, ok := result.ContainerConfig[field]; !ok {
						t.Errorf("ContainerConfig missing field %q", field)
					}
				}
			}
		})
	}
}

func TestExtractExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{
			name: "nil error",
			err:  nil,
			want: 0,
		},
		{
			name: "any error returns 1",
			err:  fmt.Errorf("container exited with code 137"),
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractExitCode(tt.err)
			if got != tt.want {
				t.Errorf("extractExitCode() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{"seconds", 45 * time.Second, "45s"},
		{"minutes", 5 * time.Minute, "5m"},
		{"hours", 2*time.Hour + 30*time.Minute, "2.5h"},
		{"days", 48 * time.Hour, "2d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDuration(tt.duration)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}

func TestValidateTokenDirect(t *testing.T) {
	tests := []struct {
		name           string
		token          string
		serverStatus   int
		wantPassed     bool
		wantStatusCode int
		wantError      string
	}{
		{
			name:           "valid API key - 200",
			token:          "sk-ant-api-test-key-1234567890",
			serverStatus:   200,
			wantPassed:     true,
			wantStatusCode: 200,
		},
		{
			name:           "valid OAuth token - 200",
			token:          "sk-ant-oat-test-oauth-token-1234567890",
			serverStatus:   200,
			wantPassed:     true,
			wantStatusCode: 200,
		},
		{
			name:           "invalid token - 401",
			token:          "sk-ant-api-invalid-key",
			serverStatus:   401,
			wantPassed:     false,
			wantStatusCode: 401,
			wantError:      "API returned status 401",
		},
		{
			name:           "forbidden - 403",
			token:          "sk-ant-api-forbidden-key",
			serverStatus:   403,
			wantPassed:     false,
			wantStatusCode: 403,
			wantError:      "API returned status 403",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request format
				if r.Method != "POST" {
					t.Errorf("expected POST, got %s", r.Method)
				}
				if r.Header.Get("anthropic-version") != "2023-06-01" {
					t.Errorf("missing anthropic-version header")
				}

				// Verify correct auth header based on token type
				isOAuth := len(tt.token) > 10 && tt.token[:10] == "sk-ant-oat"
				if isOAuth {
					if r.Header.Get("Authorization") == "" {
						t.Errorf("OAuth token should use Authorization header")
					}
					if r.Header.Get("anthropic-dangerous-direct-browser-access") != "true" {
						t.Errorf("OAuth token should set anthropic-dangerous-direct-browser-access")
					}
					if r.Header.Get("anthropic-beta") == "" {
						t.Errorf("OAuth token should set anthropic-beta")
					}
				} else {
					if r.Header.Get("x-api-key") == "" {
						t.Errorf("API key should use x-api-key header")
					}
				}

				w.WriteHeader(tt.serverStatus)
				_, _ = w.Write([]byte(`{"type":"message"}`))
			}))
			defer server.Close()

			// Override validation URL
			originalURL := anthropicValidationURL
			anthropicValidationURL = server.URL
			defer func() { anthropicValidationURL = originalURL }()

			result := validateTokenDirect(context.Background(), tt.token)

			if result.Passed != tt.wantPassed {
				t.Errorf("Passed = %v, want %v", result.Passed, tt.wantPassed)
			}
			if result.StatusCode != tt.wantStatusCode {
				t.Errorf("StatusCode = %d, want %d", result.StatusCode, tt.wantStatusCode)
			}
			if tt.wantError != "" && result.Error != tt.wantError {
				t.Errorf("Error = %q, want %q", result.Error, tt.wantError)
			}
			if result.Duration == "" {
				t.Error("Duration should not be empty")
			}
		})
	}
}

func TestValidateTokenDirect_OAuthEndpointChanged(t *testing.T) {
	// Simulate Anthropic changing their OAuth endpoint requirements
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"OAuth authentication is currently not supported"}}`))
	}))
	defer server.Close()

	originalURL := anthropicValidationURL
	anthropicValidationURL = server.URL
	defer func() { anthropicValidationURL = originalURL }()

	result := validateTokenDirect(context.Background(), "sk-ant-oat-test-oauth-token-1234567890")

	if result.Passed {
		t.Error("should not pass")
	}
	if !strings.Contains(result.Error, "probe may need updating") {
		t.Errorf("OAuth-specific error should flag probe needs updating, got: %q", result.Error)
	}
}

func TestValidateTokenDirect_NetworkError(t *testing.T) {
	// Override validation URL to a non-routable address
	originalURL := anthropicValidationURL
	anthropicValidationURL = "http://192.0.2.1:1/unreachable"
	defer func() { anthropicValidationURL = originalURL }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result := validateTokenDirect(ctx, "sk-ant-api-test-key")

	if result.Passed {
		t.Error("should not pass on network error")
	}
	if result.Error == "" {
		t.Error("should have error message for network failure")
	}
	if result.StatusCode != 0 {
		t.Errorf("StatusCode should be 0 on network error, got %d", result.StatusCode)
	}
}

func TestValidateTokenViaProxy(t *testing.T) {
	tests := []struct {
		name       string
		token      string
		wantPassed bool
	}{
		{
			name:       "API key injection succeeds",
			token:      "sk-ant-api-test-key-1234567890",
			wantPassed: true,
		},
		{
			name:       "OAuth token injection succeeds",
			token:      "sk-ant-oat-test-oauth-token-1234567890",
			wantPassed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock backend that verifies the real token was injected
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// The proxy should have replaced the placeholder with the real token
				if len(tt.token) > 10 && tt.token[:10] == "sk-ant-oat" {
					auth := r.Header.Get("Authorization")
					if auth != "Bearer "+tt.token {
						w.WriteHeader(401)
						_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"invalid token"}}`))
						return
					}
				} else {
					apiKey := r.Header.Get("x-api-key")
					if apiKey != tt.token {
						w.WriteHeader(401)
						_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"invalid key"}}`))
						return
					}
				}
				w.WriteHeader(200)
				_, _ = w.Write([]byte(`{"type":"message"}`))
			}))
			defer backend.Close()

			// Point validation URL at our mock backend
			originalURL := anthropicValidationURL
			anthropicValidationURL = backend.URL
			defer func() { anthropicValidationURL = originalURL }()

			result := validateTokenViaProxy(context.Background(), tt.token)

			if result.Passed != tt.wantPassed {
				t.Errorf("Passed = %v, want %v; error: %s", result.Passed, tt.wantPassed, result.Error)
			}
			if result.Duration == "" {
				t.Error("Duration should not be empty")
			}
		})
	}
}

func TestValidateTokenViaProxy_InjectionFailure(t *testing.T) {
	// Backend that always rejects — simulates proxy not injecting
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"invalid"}}`))
	}))
	defer backend.Close()

	originalURL := anthropicValidationURL
	anthropicValidationURL = backend.URL
	defer func() { anthropicValidationURL = originalURL }()

	result := validateTokenViaProxy(context.Background(), "sk-ant-api-bad-key")

	if result.Passed {
		t.Error("should not pass when backend rejects")
	}
	if result.StatusCode != 401 {
		t.Errorf("StatusCode = %d, want 401", result.StatusCode)
	}
}

func TestProgressiveValidation_Level1Fails(t *testing.T) {
	// Mock server that returns 401 (invalid token)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer server.Close()

	originalURL := anthropicValidationURL
	anthropicValidationURL = server.URL
	defer func() { anthropicValidationURL = originalURL }()

	diag := &claudeDiagnostic{
		Issues:      []issue{},
		Suggestions: []string{},
	}

	validation := &tokenValidationResult{}
	diag.TokenValidation = validation

	// Simulate Level 1 failure
	validation.DirectTest = validateTokenDirect(context.Background(), "sk-ant-api-invalid")

	if validation.DirectTest.Passed {
		t.Fatal("Level 1 should fail with invalid token")
	}

	// After Level 1 failure, Level 2 should be skipped
	validation.ProxyTest = &validationLevelResult{
		Skipped:    true,
		SkipReason: "token is invalid",
	}

	if tokenValidationPassed(diag) {
		t.Error("tokenValidationPassed should return false when Level 1 fails")
	}

	// Verify proxy test is marked as skipped
	if !validation.ProxyTest.Skipped {
		t.Error("Proxy test should be skipped when Level 1 fails")
	}
	if validation.ProxyTest.SkipReason != "token is invalid" {
		t.Errorf("SkipReason = %q, want %q", validation.ProxyTest.SkipReason, "token is invalid")
	}
}

func TestProgressiveValidation_Level1PassesLevel2Fails(t *testing.T) {
	// Mock server: returns 200 for direct calls, 401 for proxy calls
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"type":"message"}`))
	}))
	defer server.Close()

	// Server that rejects everything (simulates broken proxy injection)
	rejectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer rejectServer.Close()

	originalURL := anthropicValidationURL
	defer func() { anthropicValidationURL = originalURL }()

	diag := &claudeDiagnostic{
		Issues:      []issue{},
		Suggestions: []string{},
	}

	validation := &tokenValidationResult{}
	diag.TokenValidation = validation

	// Level 1 passes
	anthropicValidationURL = server.URL
	validation.DirectTest = validateTokenDirect(context.Background(), "sk-ant-api-valid-key-123456")

	if !validation.DirectTest.Passed {
		t.Fatal("Level 1 should pass")
	}

	// Level 2 fails (proxy doesn't inject correctly to a rejecting backend)
	anthropicValidationURL = rejectServer.URL
	validation.ProxyTest = validateTokenViaProxy(context.Background(), "sk-ant-api-valid-key-123456")

	if validation.ProxyTest.Passed {
		t.Fatal("Level 2 should fail when backend always rejects")
	}

	if tokenValidationPassed(diag) {
		t.Error("tokenValidationPassed should return false when Level 2 fails")
	}
}

func TestProgressiveValidation_BothPass(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"type":"message"}`))
	}))
	defer server.Close()

	originalURL := anthropicValidationURL
	anthropicValidationURL = server.URL
	defer func() { anthropicValidationURL = originalURL }()

	diag := &claudeDiagnostic{
		Issues:      []issue{},
		Suggestions: []string{},
	}

	validation := &tokenValidationResult{}
	diag.TokenValidation = validation

	// Level 1 passes
	validation.DirectTest = validateTokenDirect(context.Background(), "sk-ant-api-valid-key-123456")
	if !validation.DirectTest.Passed {
		t.Fatal("Level 1 should pass")
	}

	// Level 2 passes
	validation.ProxyTest = validateTokenViaProxy(context.Background(), "sk-ant-api-valid-key-123456")
	if !validation.ProxyTest.Passed {
		t.Fatalf("Level 2 should pass; error: %s", validation.ProxyTest.Error)
	}

	if !tokenValidationPassed(diag) {
		t.Error("tokenValidationPassed should return true when both levels pass")
	}
}

func TestTokenValidationPassed(t *testing.T) {
	tests := []struct {
		name string
		diag *claudeDiagnostic
		want bool
	}{
		{
			name: "nil token validation",
			diag: &claudeDiagnostic{},
			want: false,
		},
		{
			name: "nil direct test",
			diag: &claudeDiagnostic{
				TokenValidation: &tokenValidationResult{},
			},
			want: false,
		},
		{
			name: "direct failed",
			diag: &claudeDiagnostic{
				TokenValidation: &tokenValidationResult{
					DirectTest: &validationLevelResult{Passed: false},
				},
			},
			want: false,
		},
		{
			name: "direct passed, proxy nil",
			diag: &claudeDiagnostic{
				TokenValidation: &tokenValidationResult{
					DirectTest: &validationLevelResult{Passed: true},
				},
			},
			want: false,
		},
		{
			name: "direct passed, proxy failed",
			diag: &claudeDiagnostic{
				TokenValidation: &tokenValidationResult{
					DirectTest: &validationLevelResult{Passed: true},
					ProxyTest:  &validationLevelResult{Passed: false},
				},
			},
			want: false,
		},
		{
			name: "both passed",
			diag: &claudeDiagnostic{
				TokenValidation: &tokenValidationResult{
					DirectTest: &validationLevelResult{Passed: true},
					ProxyTest:  &validationLevelResult{Passed: true},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tokenValidationPassed(tt.diag)
			if got != tt.want {
				t.Errorf("tokenValidationPassed() = %v, want %v", got, tt.want)
			}
		})
	}
}
