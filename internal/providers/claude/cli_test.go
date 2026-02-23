package claude

import (
	"crypto/rand"
	"os"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/credential"
)

func newTestStore(t *testing.T) *credential.FileStore {
	t.Helper()
	dir := t.TempDir()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	store, err := credential.NewFileStore(dir, key)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestResolveClaudeCredential_PrefersClaude(t *testing.T) {
	store := newTestStore(t)

	// Store both claude and anthropic credentials
	if err := store.Save(credential.Credential{
		Provider:  credential.ProviderClaude,
		Token:     "sk-ant-oat01-test-oauth-token",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(credential.Credential{
		Provider:  credential.ProviderAnthropic,
		Token:     "sk-ant-api03-test-api-key",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	got := resolveClaudeCredential(store)
	if got != "claude" {
		t.Errorf("resolveClaudeCredential() = %q, want %q", got, "claude")
	}
}

func TestResolveClaudeCredential_FallsBackToAnthropic(t *testing.T) {
	store := newTestStore(t)

	// Only anthropic API key exists
	if err := store.Save(credential.Credential{
		Provider:  credential.ProviderAnthropic,
		Token:     "sk-ant-api03-test-api-key",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	got := resolveClaudeCredential(store)
	if got != "anthropic" {
		t.Errorf("resolveClaudeCredential() = %q, want %q", got, "anthropic")
	}

	// Verify anthropic credential is untouched (not migrated)
	cred, err := store.Get(credential.ProviderAnthropic)
	if err != nil {
		t.Fatal("anthropic credential should still exist")
	}
	if cred.Token != "sk-ant-api03-test-api-key" {
		t.Errorf("anthropic token changed unexpectedly")
	}
}

func TestResolveClaudeCredential_MigratesClaudeOAuth(t *testing.T) {
	store := newTestStore(t)

	// Store credential under legacy "claude-oauth" name
	if err := store.Save(credential.Credential{
		Provider:  "claude-oauth",
		Token:     "sk-ant-oat01-test-oauth-token",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	got := resolveClaudeCredential(store)
	if got != "claude" {
		t.Errorf("resolveClaudeCredential() = %q, want %q", got, "claude")
	}

	// Verify migration: claude.enc should exist, claude-oauth.enc should not
	cred, err := store.Get(credential.ProviderClaude)
	if err != nil {
		t.Fatal("claude credential should exist after migration")
	}
	if cred.Token != "sk-ant-oat01-test-oauth-token" {
		t.Errorf("migrated token = %q, want original", cred.Token)
	}

	_, err = store.Get("claude-oauth")
	if err == nil {
		t.Error("claude-oauth credential should have been deleted after migration")
	}
}

func TestResolveClaudeCredential_MigratesOAuthFromAnthropic(t *testing.T) {
	store := newTestStore(t)

	// Store OAuth token under "anthropic" (legacy state)
	if err := store.Save(credential.Credential{
		Provider:  credential.ProviderAnthropic,
		Token:     "sk-ant-oat01-test-oauth-token",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	got := resolveClaudeCredential(store)
	if got != "claude" {
		t.Errorf("resolveClaudeCredential() = %q, want %q", got, "claude")
	}

	// Verify migration: claude.enc should exist, anthropic.enc should not
	cred, err := store.Get(credential.ProviderClaude)
	if err != nil {
		t.Fatal("claude credential should exist after migration")
	}
	if cred.Token != "sk-ant-oat01-test-oauth-token" {
		t.Errorf("migrated token = %q, want original", cred.Token)
	}

	_, err = store.Get(credential.ProviderAnthropic)
	if err == nil {
		t.Error("anthropic credential should have been deleted after migration")
	}
}

func TestResolveClaudeCredential_NoCredentials(t *testing.T) {
	store := newTestStore(t)

	got := resolveClaudeCredential(store)
	if got != "" {
		t.Errorf("resolveClaudeCredential() = %q, want empty string", got)
	}
}

func TestResolveClaudeCredential_Idempotent(t *testing.T) {
	store := newTestStore(t)

	// Store OAuth token under "anthropic" to trigger migration
	if err := store.Save(credential.Credential{
		Provider:  credential.ProviderAnthropic,
		Token:     "sk-ant-oat01-test-oauth-token",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	// First call triggers migration
	got1 := resolveClaudeCredential(store)
	if got1 != "claude" {
		t.Errorf("first call = %q, want %q", got1, "claude")
	}

	// Second call should hit the fast path (no migration needed)
	got2 := resolveClaudeCredential(store)
	if got2 != "claude" {
		t.Errorf("second call = %q, want %q", got2, "claude")
	}

	// Verify only claude.enc exists
	creds, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 1 {
		t.Errorf("expected 1 credential after migration, got %d", len(creds))
	}
	if len(creds) > 0 && creds[0].Provider != credential.ProviderClaude {
		t.Errorf("remaining credential provider = %q, want %q", creds[0].Provider, credential.ProviderClaude)
	}
}

// TestResolveClaudeCredential_OrphanedOldCredential verifies that if Delete
// fails after Save succeeds during migration, the function still returns the
// correct grant name. The orphaned old credential is harmless — the next call
// will hit the fast path.
func TestResolveClaudeCredential_OrphanedOldCredential(t *testing.T) {
	store := newTestStore(t)

	// Simulate the state after a partial migration: both claude.enc and
	// claude-oauth.enc exist (delete failed after save).
	if err := store.Save(credential.Credential{
		Provider:  credential.ProviderClaude,
		Token:     "sk-ant-oat01-test-oauth-token",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(credential.Credential{
		Provider:  "claude-oauth",
		Token:     "sk-ant-oat01-test-oauth-token",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	// Should return "claude" via fast path, ignoring the orphan
	got := resolveClaudeCredential(store)
	if got != "claude" {
		t.Errorf("resolveClaudeCredential() = %q, want %q", got, "claude")
	}
}

func TestResolveClaudeCredential_SkipsMigrationInReadOnlyDir(t *testing.T) {
	// Create store in a directory we'll make read-only
	dir := t.TempDir()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	store, err := credential.NewFileStore(dir, key)
	if err != nil {
		t.Fatal(err)
	}

	// Store OAuth token under anthropic
	if err := store.Save(credential.Credential{
		Provider:  credential.ProviderAnthropic,
		Token:     "sk-ant-oat01-test-oauth-token",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	// Make directory read-only to prevent migration writes
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0755) // restore for cleanup

	// Should fall through to "anthropic" since migration can't write
	got := resolveClaudeCredential(store)
	if got != "anthropic" {
		t.Errorf("resolveClaudeCredential() = %q, want %q (migration should fail gracefully)", got, "anthropic")
	}
}
