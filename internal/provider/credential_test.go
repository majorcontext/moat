package provider

import (
	"testing"
	"time"
)

// mockLegacyCredential implements LegacyCredential for testing.
type mockLegacyCredential struct {
	provider  string
	token     string
	scopes    []string
	expiresAt time.Time
	createdAt time.Time
	metadata  map[string]string
}

func (m *mockLegacyCredential) GetProvider() string            { return m.provider }
func (m *mockLegacyCredential) GetToken() string               { return m.token }
func (m *mockLegacyCredential) GetScopes() []string            { return m.scopes }
func (m *mockLegacyCredential) GetExpiresAt() time.Time        { return m.expiresAt }
func (m *mockLegacyCredential) GetCreatedAt() time.Time        { return m.createdAt }
func (m *mockLegacyCredential) GetMetadata() map[string]string { return m.metadata }

func TestFromLegacy(t *testing.T) {
	t.Run("nil returns nil", func(t *testing.T) {
		got := FromLegacy(nil)
		if got != nil {
			t.Errorf("FromLegacy(nil) = %v, want nil", got)
		}
	})

	t.Run("converts all fields", func(t *testing.T) {
		now := time.Now()
		expires := now.Add(time.Hour)
		legacy := &mockLegacyCredential{
			provider:  "github",
			token:     "ghp_test123",
			scopes:    []string{"repo", "read:user"},
			expiresAt: expires,
			createdAt: now,
			metadata:  map[string]string{"source": "cli"},
		}

		got := FromLegacy(legacy)

		if got == nil {
			t.Fatal("FromLegacy() returned nil")
		}
		if got.Provider != "github" {
			t.Errorf("Provider = %q, want 'github'", got.Provider)
		}
		if got.Token != "ghp_test123" {
			t.Errorf("Token = %q, want 'ghp_test123'", got.Token)
		}
		if len(got.Scopes) != 2 || got.Scopes[0] != "repo" {
			t.Errorf("Scopes = %v, want [repo, read:user]", got.Scopes)
		}
		if !got.ExpiresAt.Equal(expires) {
			t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, expires)
		}
		if !got.CreatedAt.Equal(now) {
			t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, now)
		}
		if got.Metadata["source"] != "cli" {
			t.Errorf("Metadata[source] = %q, want 'cli'", got.Metadata["source"])
		}
	})

	t.Run("handles empty fields", func(t *testing.T) {
		legacy := &mockLegacyCredential{
			provider: "test",
			token:    "token",
		}

		got := FromLegacy(legacy)

		if got == nil {
			t.Fatal("FromLegacy() returned nil")
		}
		if got.Scopes != nil {
			t.Errorf("Scopes = %v, want nil", got.Scopes)
		}
		if got.Metadata != nil {
			t.Errorf("Metadata = %v, want nil", got.Metadata)
		}
	})
}
