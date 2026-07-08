package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func withCopilotValidationServer(t *testing.T, status int, body string, check func(*testing.T, *http.Request)) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			check(t, r)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	origURL := copilotValidationURL
	origClient := newCopilotHTTPClient
	copilotValidationURL = server.URL
	newCopilotHTTPClient = server.Client
	t.Cleanup(func() {
		copilotValidationURL = origURL
		newCopilotHTTPClient = origClient
	})
}

func TestValidateCopilotToken(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{name: "success", status: http.StatusOK},
		{name: "unauthorized", status: http.StatusUnauthorized, wantErr: "invalid token"},
		{name: "forbidden message", status: http.StatusForbidden, body: `{"message":"missing Copilot Requests"}`, wantErr: "missing Copilot Requests"},
		{name: "forbidden generic", status: http.StatusForbidden, wantErr: "token rejected"},
		{name: "server error message", status: http.StatusInternalServerError, body: `{"message":"boom"}`, wantErr: "500: boom"},
		{name: "server error generic", status: http.StatusInternalServerError, wantErr: "unexpected status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withCopilotValidationServer(t, tt.status, tt.body, func(t *testing.T, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
					t.Errorf("Authorization = %q", got)
				}
				if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
					t.Errorf("Accept = %q", got)
				}
				if got := r.Header.Get("User-Agent"); got != "moat" {
					t.Errorf("User-Agent = %q", got)
				}
			})

			err := ValidateGitHubToken(context.Background(), "test-token")
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateCopilotToken() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateCopilotToken() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidationResponseBodyNeedNotBeJSON(t *testing.T) {
	withCopilotValidationServer(t, http.StatusInternalServerError, `not-json`, nil)
	err := ValidateGitHubToken(context.Background(), "test-token")
	if err == nil || !strings.Contains(err.Error(), "unexpected status validating token: 500") {
		t.Fatalf("validateCopilotToken() error = %v", err)
	}
}

func TestValidationMessageJSON(t *testing.T) {
	body, err := json.Marshal(map[string]string{"message": "rate limited"})
	if err != nil {
		t.Fatal(err)
	}
	withCopilotValidationServer(t, http.StatusForbidden, string(body), nil)
	err = ValidateGitHubToken(context.Background(), "test-token")
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("validateCopilotToken() error = %v", err)
	}
}

func TestGrantUsesGitHubCredential(t *testing.T) {
	_, err := (&Provider{}).Grant(context.Background())
	if err == nil {
		t.Fatal("Grant() = nil, want error")
	}
	if !strings.Contains(err.Error(), "moat grant github") {
		t.Fatalf("Grant() error = %v, want moat grant github guidance", err)
	}
}
