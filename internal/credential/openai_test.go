package credential

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAIAuth_ValidateKey(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantErr    bool
		errContain string
	}{
		{
			name:       "valid key",
			statusCode: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "invalid key (unauthorized)",
			statusCode: http.StatusUnauthorized,
			wantErr:    true,
			errContain: "invalid API key",
		},
		{
			name:       "forbidden (insufficient permissions)",
			statusCode: http.StatusForbidden,
			wantErr:    true,
			errContain: "lacks required permissions",
		},
		{
			name:       "rate limited",
			statusCode: http.StatusTooManyRequests,
			wantErr:    true,
			errContain: "rate limited",
		},
		{
			name:       "server error",
			statusCode: http.StatusInternalServerError,
			wantErr:    true,
			errContain: "API error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify Authorization header is set
				auth := r.Header.Get("Authorization")
				if auth == "" {
					t.Error("Authorization header not set")
				}
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			auth := &OpenAIAuth{APIURL: server.URL}
			err := auth.ValidateKey(context.Background(), "sk-test-key")

			if tt.wantErr {
				if err == nil {
					t.Error("ValidateKey() expected error, got nil")
				} else if tt.errContain != "" && !strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("ValidateKey() error = %v, want error containing %q", err, tt.errContain)
				}
			} else if err != nil {
				t.Errorf("ValidateKey() unexpected error: %v", err)
			}
		})
	}
}

func TestOpenAIAuth_CreateCredential(t *testing.T) {
	auth := &OpenAIAuth{}
	apiKey := "sk-test-key-12345"

	cred := auth.CreateCredential(apiKey)

	if cred.Provider != ProviderOpenAI {
		t.Errorf("Provider = %q, want %q", cred.Provider, ProviderOpenAI)
	}
	if cred.Token != apiKey {
		t.Errorf("Token = %q, want %q", cred.Token, apiKey)
	}
	if cred.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	// Verify CreatedAt is recent (within last minute)
	if time.Since(cred.CreatedAt) > time.Minute {
		t.Error("CreatedAt should be recent")
	}
}
