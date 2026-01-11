package credential

import (
	"testing"
	"time"
)

func TestFileStore_SaveAndGet(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, []byte("test-encryption-key-32-bytes!!ab"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	cred := Credential{
		Provider:  ProviderGitHub,
		Token:     "ghp_test123",
		Scopes:    []string{"repo", "read:user"},
		CreatedAt: time.Now(),
	}

	if err := store.Save(cred); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(ProviderGitHub)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Token != cred.Token {
		t.Errorf("Token = %q, want %q", got.Token, cred.Token)
	}
}

func TestFileStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, []byte("test-encryption-key-32-bytes!!ab"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	cred := Credential{
		Provider:  ProviderGitHub,
		Token:     "ghp_test123",
		CreatedAt: time.Now(),
	}

	store.Save(cred)
	if err := store.Delete(ProviderGitHub); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = store.Get(ProviderGitHub)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestFileStore_GetNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, []byte("test-encryption-key-32-bytes!!ab"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	_, err = store.Get(ProviderGitHub)
	if err == nil {
		t.Error("expected error for non-existent credential")
	}
}

func TestFileStore_List(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, []byte("test-encryption-key-32-bytes!!ab"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Initially empty
	creds, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(creds) != 0 {
		t.Errorf("List() = %d credentials, want 0", len(creds))
	}

	// Add two credentials
	store.Save(Credential{
		Provider:  ProviderGitHub,
		Token:     "ghp_test123",
		CreatedAt: time.Now(),
	})
	store.Save(Credential{
		Provider:  ProviderAWS,
		Token:     "aws_test456",
		CreatedAt: time.Now(),
	})

	creds, err = store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(creds) != 2 {
		t.Errorf("List() = %d credentials, want 2", len(creds))
	}
}

func TestNewFileStore_InvalidKeyLength(t *testing.T) {
	dir := t.TempDir()
	_, err := NewFileStore(dir, []byte("short-key"))
	if err == nil {
		t.Error("expected error for invalid key length")
	}
}
