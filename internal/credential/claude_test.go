package credential

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeOAuthToken_ExpiresAtTime(t *testing.T) {
	// Test with a known timestamp (milliseconds)
	token := &ClaudeOAuthToken{
		ExpiresAt: 1768957840059, // Jan 21, 2026 or so
	}

	got := token.ExpiresAtTime()
	if got.IsZero() {
		t.Error("ExpiresAtTime() returned zero time")
	}

	// Verify it's roughly the expected time (within a day to be safe)
	expected := time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)
	if got.Before(expected.Add(-24*time.Hour)) || got.After(expected.Add(48*time.Hour)) {
		t.Errorf("ExpiresAtTime() = %v, expected around %v", got, expected)
	}
}

func TestClaudeOAuthToken_IsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt int64
		want      bool
	}{
		{
			name:      "expired token",
			expiresAt: time.Now().Add(-1 * time.Hour).UnixMilli(),
			want:      true,
		},
		{
			name:      "valid token",
			expiresAt: time.Now().Add(1 * time.Hour).UnixMilli(),
			want:      false,
		},
		{
			name:      "zero expiration",
			expiresAt: 0,
			want:      true, // Zero time is in the past
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := &ClaudeOAuthToken{
				ExpiresAt: tt.expiresAt,
			}
			if got := token.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClaudeCodeCredentials_GetFromFile(t *testing.T) {
	// Create a temp directory for test credentials
	tempDir := t.TempDir()

	// Create .claude directory
	claudeDir := filepath.Join(tempDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	// Write test credentials file
	credFile := filepath.Join(claudeDir, ".credentials.json")
	testCreds := `{
		"claudeAiOauth": {
			"accessToken": "sk-ant-oat01-test-token",
			"refreshToken": "sk-ant-ort01-test-refresh",
			"expiresAt": 1768957840059,
			"scopes": ["user:inference", "user:profile"],
			"subscriptionType": "max",
			"rateLimitTier": "default"
		}
	}`
	if err := os.WriteFile(credFile, []byte(testCreds), 0600); err != nil {
		t.Fatalf("Failed to write test credentials: %v", err)
	}

	// Override home directory for test
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tempDir)
	defer os.Setenv("HOME", originalHome)

	c := &ClaudeCodeCredentials{}
	token, err := c.getFromFile()
	if err != nil {
		t.Fatalf("getFromFile() error = %v", err)
	}

	if token.AccessToken != "sk-ant-oat01-test-token" {
		t.Errorf("AccessToken = %q, want %q", token.AccessToken, "sk-ant-oat01-test-token")
	}
	if token.RefreshToken != "sk-ant-ort01-test-refresh" {
		t.Errorf("RefreshToken = %q, want %q", token.RefreshToken, "sk-ant-ort01-test-refresh")
	}
	if token.SubscriptionType != "max" {
		t.Errorf("SubscriptionType = %q, want %q", token.SubscriptionType, "max")
	}
	if len(token.Scopes) != 2 {
		t.Errorf("Scopes = %v, want 2 scopes", token.Scopes)
	}
}

func TestClaudeCodeCredentials_GetFromFile_NotFound(t *testing.T) {
	// Create a temp directory without any credentials
	tempDir := t.TempDir()

	// Override home directory for test
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tempDir)
	defer os.Setenv("HOME", originalHome)

	c := &ClaudeCodeCredentials{}
	_, err := c.getFromFile()
	if err == nil {
		t.Error("getFromFile() expected error for missing file")
	}
}

func TestClaudeCodeCredentials_GetFromFile_NoOAuth(t *testing.T) {
	// Create a temp directory with credentials file but no OAuth
	tempDir := t.TempDir()

	claudeDir := filepath.Join(tempDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	credFile := filepath.Join(claudeDir, ".credentials.json")
	if err := os.WriteFile(credFile, []byte(`{}`), 0600); err != nil {
		t.Fatalf("Failed to write test credentials: %v", err)
	}

	// Override home directory for test
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tempDir)
	defer os.Setenv("HOME", originalHome)

	c := &ClaudeCodeCredentials{}
	_, err := c.getFromFile()
	if err == nil {
		t.Error("getFromFile() expected error for empty OAuth")
	}
}

func TestClaudeCodeCredentials_CreateCredentialFromOAuth(t *testing.T) {
	token := &ClaudeOAuthToken{
		AccessToken:      "sk-ant-oat01-test-token",
		RefreshToken:     "sk-ant-ort01-test-refresh",
		ExpiresAt:        time.Now().Add(1 * time.Hour).UnixMilli(),
		Scopes:           []string{"user:inference", "user:profile"},
		SubscriptionType: "max",
	}

	c := &ClaudeCodeCredentials{}
	cred := c.CreateCredentialFromOAuth(token)

	if cred.Provider != ProviderAnthropic {
		t.Errorf("Provider = %q, want %q", cred.Provider, ProviderAnthropic)
	}
	if cred.Token != "sk-ant-oat01-test-token" {
		t.Errorf("Token = %q, want %q", cred.Token, "sk-ant-oat01-test-token")
	}
	if len(cred.Scopes) != 2 {
		t.Errorf("Scopes = %v, want 2 scopes", cred.Scopes)
	}
	if cred.ExpiresAt.IsZero() {
		t.Error("ExpiresAt should not be zero")
	}
	if cred.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
}

func TestClaudeCodeCredentials_HasClaudeCodeCredentials(t *testing.T) {
	// Create a temp directory with valid credentials
	tempDir := t.TempDir()

	claudeDir := filepath.Join(tempDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	credFile := filepath.Join(claudeDir, ".credentials.json")
	testCreds := `{"claudeAiOauth": {"accessToken": "test", "expiresAt": 1768957840059}}`
	if err := os.WriteFile(credFile, []byte(testCreds), 0600); err != nil {
		t.Fatalf("Failed to write test credentials: %v", err)
	}

	// Override home directory for test
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tempDir)
	defer os.Setenv("HOME", originalHome)

	c := &ClaudeCodeCredentials{}
	if !c.HasClaudeCodeCredentials() {
		t.Error("HasClaudeCodeCredentials() = false, want true")
	}
}
