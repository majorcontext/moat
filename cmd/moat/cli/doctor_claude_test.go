package cli

import (
	"fmt"
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
