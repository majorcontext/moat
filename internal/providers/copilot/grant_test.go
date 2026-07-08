package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
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

			err := validateCopilotToken(context.Background(), "test-token")
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

func TestValidateAndCreateCredential(t *testing.T) {
	withCopilotValidationServer(t, http.StatusOK, `{}`, nil)

	cred, err := validateAndCreateCredential(context.Background(), "token", SourceEnv)
	if err != nil {
		t.Fatalf("validateAndCreateCredential() error = %v", err)
	}
	if cred.Provider != "github" || cred.Token != "token" {
		t.Fatalf("credential = %+v", cred)
	}
	if got := cred.Metadata[provider.MetaKeyTokenSource]; got != SourceEnv {
		t.Fatalf("token source = %q, want %q", got, SourceEnv)
	}
}

func TestValidateAndCreateCredentialError(t *testing.T) {
	withCopilotValidationServer(t, http.StatusUnauthorized, `{}`, nil)

	_, err := validateAndCreateCredential(context.Background(), "bad-token", SourcePAT)
	var grantErr *provider.GrantError
	if !errors.As(err, &grantErr) {
		t.Fatalf("error = %T %v, want GrantError", err, err)
	}
	if grantErr.Provider != copilotProviderName {
		t.Fatalf("GrantError.Provider = %q", grantErr.Provider)
	}
}

func TestGrantExecuteEnvToken(t *testing.T) {
	withCopilotValidationServer(t, http.StatusOK, `{}`, nil)
	t.Setenv("COPILOT_GITHUB_TOKEN", "env-token")

	cred, err := NewGrant().Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if cred.Token != "env-token" {
		t.Fatalf("token = %q, want env-token", cred.Token)
	}
	if cred.Provider != "github" {
		t.Fatalf("provider = %q, want github", cred.Provider)
	}
	if got := cred.Metadata[provider.MetaKeyTokenSource]; got != SourceEnv {
		t.Fatalf("token source = %q, want %q", got, SourceEnv)
	}
}

func TestValidationResponseBodyNeedNotBeJSON(t *testing.T) {
	withCopilotValidationServer(t, http.StatusInternalServerError, `not-json`, nil)
	err := validateCopilotToken(context.Background(), "test-token")
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
	err = validateCopilotToken(context.Background(), "test-token")
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("validateCopilotToken() error = %v", err)
	}
}
