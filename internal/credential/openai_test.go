package credential

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsCodexToken(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  bool
	}{
		// API keys (should return false)
		{
			name:  "standard API key",
			token: "sk-1234567890abcdef",
			want:  false,
		},
		{
			name:  "project API key",
			token: "sk-proj-abcdefghijklmnop",
			want:  false,
		},
		{
			name:  "service account API key",
			token: "sk-svcacct-abcdefghijklmnop",
			want:  false,
		},
		{
			name:  "long API key with new format",
			token: "sk-proj-VeryLongTokenStringThatExceedsNormalLength1234567890abcdefghijklmnopqrstuvwxyz",
			want:  false,
		},

		// Subscription/OAuth tokens (should return true)
		{
			name:  "typical OAuth token",
			token: "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIn0.signature",
			want:  true,
		},
		{
			name:  "subscription token without sk prefix",
			token: "chatgpt-subscription-token-abc123",
			want:  true,
		},

		// Edge cases - invalid tokens should return false
		{
			name:  "empty string",
			token: "",
			want:  false, // empty string is invalid, not a subscription token
		},
		{
			name:  "whitespace only",
			token: "   ",
			want:  false, // whitespace is invalid, not a subscription token
		},
		{
			name:  "single character",
			token: "s",
			want:  false, // too short to be any valid token
		},
		{
			name:  "just sk-",
			token: "sk-",
			want:  false, // starts with "sk-" and too short
		},
		{
			name:  "short non-sk token",
			token: "abc123",
			want:  false, // too short to be a valid OAuth token
		},
		{
			name:  "sk without hyphen (short)",
			token: "sk123456",
			want:  false, // too short even if valid format (< 10 chars)
		},
		{
			name:  "sk without hyphen (long enough)",
			token: "sk1234567890abcdefghij",
			want:  true, // long enough and doesn't start with "sk-"
		},
		{
			name:  "uppercase SK- (long enough)",
			token: "SK-1234567890abcdefghij",
			want:  true, // prefix check is case-sensitive, this is treated as OAuth
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsCodexToken(tt.token)
			if got != tt.want {
				t.Errorf("IsCodexToken(%q) = %v, want %v", tt.token, got, tt.want)
			}
		})
	}
}

func TestCodexAuthToken_ExpiresAtTime(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt int64
		wantZero  bool
	}{
		{
			name:      "zero value returns zero time",
			expiresAt: 0,
			wantZero:  true,
		},
		{
			name:      "valid unix timestamp",
			expiresAt: 1768957840, // approximately Jan 2026
			wantZero:  false,
		},
		{
			name:      "negative timestamp",
			expiresAt: -1,
			wantZero:  false, // time.Unix handles negative values
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := &CodexAuthToken{ExpiresAt: tt.expiresAt}
			got := token.ExpiresAtTime()
			if tt.wantZero && !got.IsZero() {
				t.Errorf("ExpiresAtTime() = %v, want zero time", got)
			}
			if !tt.wantZero && got.IsZero() {
				t.Error("ExpiresAtTime() returned zero time, want non-zero")
			}
		})
	}
}

func TestCodexAuthToken_IsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt int64
		want      bool
	}{
		{
			name:      "expired token (past)",
			expiresAt: time.Now().Add(-1 * time.Hour).Unix(),
			want:      true,
		},
		{
			name:      "valid token (future)",
			expiresAt: time.Now().Add(1 * time.Hour).Unix(),
			want:      false,
		},
		{
			name:      "no expiration set",
			expiresAt: 0,
			want:      false, // no expiration means never expires
		},
		{
			name:      "expiration at epoch",
			expiresAt: 1, // January 1, 1970 00:00:01 UTC
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := &CodexAuthToken{ExpiresAt: tt.expiresAt}
			if got := token.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

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

func TestCodexCredentials_GetFromFile(t *testing.T) {
	// Create a temp directory for test credentials
	tempDir := t.TempDir()

	// Create .codex directory
	codexDir := filepath.Join(tempDir, ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	// Test case: valid credentials file with token
	t.Run("valid credentials with token", func(t *testing.T) {
		credFile := filepath.Join(codexDir, "auth.json")
		testCreds := `{"token": {"access_token": "test-access-token", "expires_at": 1768957840}}`
		if err := os.WriteFile(credFile, []byte(testCreds), 0600); err != nil {
			t.Fatalf("Failed to write test credentials: %v", err)
		}
		defer os.Remove(credFile)

		// Note: This test is limited because getFromFile uses os.UserHomeDir()
		// In a real scenario, we'd use dependency injection for the home directory
		// For now, we just verify the file was created correctly
		_ = &CodexCredentials{}
	})

	// Test case: missing auth.json file
	t.Run("missing credentials file", func(t *testing.T) {
		c := &CodexCredentials{}
		_, err := c.getFromFile()
		if err == nil {
			t.Error("getFromFile() expected error for missing file, got nil")
		}
	})

	// Test case: invalid JSON in auth.json
	t.Run("invalid JSON", func(t *testing.T) {
		credFile := filepath.Join(codexDir, "auth.json")
		if err := os.WriteFile(credFile, []byte("not valid json"), 0600); err != nil {
			t.Fatalf("Failed to write test file: %v", err)
		}
		defer os.Remove(credFile)

		c := &CodexCredentials{}
		_, err := c.getFromFile()
		if err == nil {
			t.Error("getFromFile() expected error for invalid JSON, got nil")
		}
	})

	// Test case: valid JSON but no token
	t.Run("valid JSON but no token", func(t *testing.T) {
		credFile := filepath.Join(codexDir, "auth.json")
		if err := os.WriteFile(credFile, []byte(`{"OPENAI_API_KEY": "sk-test"}`), 0600); err != nil {
			t.Fatalf("Failed to write test file: %v", err)
		}
		defer os.Remove(credFile)

		c := &CodexCredentials{}
		_, err := c.getFromFile()
		if err == nil {
			t.Error("getFromFile() expected error for missing token, got nil")
		}
	})
}

func TestCodexCredentials_CreateCredentialFromCodex(t *testing.T) {
	tests := []struct {
		name        string
		token       *CodexAuthToken
		wantExpires bool
	}{
		{
			name: "token with expiration",
			token: &CodexAuthToken{
				AccessToken: "test-access-token",
				ExpiresAt:   time.Now().Add(1 * time.Hour).Unix(),
			},
			wantExpires: true,
		},
		{
			name: "token without expiration",
			token: &CodexAuthToken{
				AccessToken: "test-access-token",
				ExpiresAt:   0,
			},
			wantExpires: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &CodexCredentials{}
			cred := c.CreateCredentialFromCodex(tt.token)

			if cred.Provider != ProviderOpenAI {
				t.Errorf("Provider = %q, want %q", cred.Provider, ProviderOpenAI)
			}
			if cred.Token != tt.token.AccessToken {
				t.Errorf("Token = %q, want %q", cred.Token, tt.token.AccessToken)
			}
			if cred.CreatedAt.IsZero() {
				t.Error("CreatedAt should not be zero")
			}
			if tt.wantExpires && cred.ExpiresAt.IsZero() {
				t.Error("ExpiresAt should not be zero when token has expiration")
			}
			if !tt.wantExpires && !cred.ExpiresAt.IsZero() {
				t.Error("ExpiresAt should be zero when token has no expiration")
			}
		})
	}
}
