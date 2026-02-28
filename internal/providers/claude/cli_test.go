package claude

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/storage"
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

// writeTestMetadata writes a metadata.json file to the run directory.
func writeTestMetadata(t *testing.T, baseDir, runID string, meta storage.Metadata) {
	t.Helper()
	store, err := storage.NewRunStore(baseDir, runID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Dir()+"/metadata.json", data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveResumeSession_RawUUID(t *testing.T) {
	uuid := "b281f735-7d2b-4979-95de-0e2a7a9c2315"
	got, err := resolveResumeSession(uuid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != uuid {
		t.Errorf("got %q, want %q", got, uuid)
	}
}

func TestResolveResumeSession_ByRunName(t *testing.T) {
	baseDir := t.TempDir()

	writeTestMetadata(t, baseDir, "run_abc123def456", storage.Metadata{
		Name:         "my-feature",
		ContainerID:  "abc",
		ProviderMeta: map[string]string{"claude_session_id": "aaaabbbb-1111-2222-3333-444455556666"},
		CreatedAt:    time.Now(),
	})

	got, err := resolveResumeSessionInDir("my-feature", baseDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "aaaabbbb-1111-2222-3333-444455556666" {
		t.Errorf("got %q, want session UUID", got)
	}
}

func TestResolveResumeSession_ByRunID(t *testing.T) {
	baseDir := t.TempDir()

	writeTestMetadata(t, baseDir, "run_abc123def456", storage.Metadata{
		Name:         "my-feature",
		ContainerID:  "abc",
		ProviderMeta: map[string]string{"claude_session_id": "aaaabbbb-1111-2222-3333-444455556666"},
		CreatedAt:    time.Now(),
	})

	got, err := resolveResumeSessionInDir("run_abc123def456", baseDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "aaaabbbb-1111-2222-3333-444455556666" {
		t.Errorf("got %q, want session UUID", got)
	}
}

func TestResolveResumeSession_NoSessionID(t *testing.T) {
	baseDir := t.TempDir()

	writeTestMetadata(t, baseDir, "run_abc123def456", storage.Metadata{
		Name:        "my-feature",
		ContainerID: "abc",
		CreatedAt:   time.Now(),
	})

	_, err := resolveResumeSessionInDir("my-feature", baseDir)
	if err == nil {
		t.Fatal("expected error for missing session ID")
	}
	if !strings.Contains(err.Error(), "no recorded Claude session ID") {
		t.Errorf("error = %q, want mention of 'no recorded Claude session ID'", err)
	}
}

func TestResolveResumeSession_StillRunning(t *testing.T) {
	baseDir := t.TempDir()

	writeTestMetadata(t, baseDir, "run_abc123def456", storage.Metadata{
		Name:        "my-feature",
		ContainerID: "abc",
		State:       "running",
		CreatedAt:   time.Now(),
	})

	_, err := resolveResumeSessionInDir("my-feature", baseDir)
	if err == nil {
		t.Fatal("expected error for running run")
	}
	if !strings.Contains(err.Error(), "still running") {
		t.Errorf("error = %q, want mention of 'still running'", err)
	}
	if !strings.Contains(err.Error(), "moat logs") {
		t.Errorf("error = %q, want mention of 'moat logs'", err)
	}
}

func TestResolveResumeSession_NotFound(t *testing.T) {
	baseDir := t.TempDir()

	_, err := resolveResumeSessionInDir("nonexistent", baseDir)
	if err == nil {
		t.Fatal("expected error for nonexistent run")
	}
	if !strings.Contains(err.Error(), "no run found") {
		t.Errorf("error = %q, want mention of 'no run found'", err)
	}
}

func TestResolveResumeSession_MostRecentNameWins(t *testing.T) {
	baseDir := t.TempDir()

	// Two runs with the same name, different session IDs
	writeTestMetadata(t, baseDir, "run_aaa111bbb222", storage.Metadata{
		Name:         "my-feature",
		ContainerID:  "old",
		ProviderMeta: map[string]string{"claude_session_id": "old-session-1111-2222-3333-444455556666"},
		CreatedAt:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	writeTestMetadata(t, baseDir, "run_ccc333ddd444", storage.Metadata{
		Name:         "my-feature",
		ContainerID:  "new",
		ProviderMeta: map[string]string{"claude_session_id": "new-session-1111-2222-3333-444455556666"},
		CreatedAt:    time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
	})

	got, err := resolveResumeSessionInDir("my-feature", baseDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "new-session-1111-2222-3333-444455556666" {
		t.Errorf("got %q, want most recent session UUID", got)
	}
}
