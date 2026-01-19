package keyring

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockBackend for testing fallback behavior
type mockBackend struct {
	key      []byte
	getErr   error
	setErr   error
	getCalls int
	setCalls int
}

func (m *mockBackend) Get() ([]byte, error) {
	m.getCalls++
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.key == nil {
		return nil, fmt.Errorf("key not found")
	}
	return m.key, nil
}

func (m *mockBackend) Set(key []byte) error {
	m.setCalls++
	if m.setErr != nil {
		return m.setErr
	}
	m.key = key
	return nil
}

func (m *mockBackend) Delete() error {
	m.key = nil
	return nil
}

func (m *mockBackend) Name() string {
	return "mock"
}

func TestGetOrCreateKeyExisting(t *testing.T) {
	existingKey := make([]byte, 32)
	if _, err := rand.Read(existingKey); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	primary := &mockBackend{key: existingKey}
	fallback := &mockBackend{}

	key, err := getOrCreateKeyWithBackends(primary, fallback)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Equal(key, existingKey) {
		t.Error("should return existing key from primary")
	}
	if primary.getCalls != 1 {
		t.Errorf("primary.Get called %d times, want 1", primary.getCalls)
	}
	if fallback.getCalls != 0 {
		t.Error("fallback should not be checked when primary succeeds")
	}
}

func TestGetOrCreateKeyFallbackExisting(t *testing.T) {
	existingKey := make([]byte, 32)
	if _, err := rand.Read(existingKey); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	primary := &mockBackend{getErr: fmt.Errorf("keychain unavailable")}
	fallback := &mockBackend{key: existingKey}

	key, err := getOrCreateKeyWithBackends(primary, fallback)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Equal(key, existingKey) {
		t.Error("should return existing key from fallback")
	}
}

func TestGetOrCreateKeyGeneratesNew(t *testing.T) {
	primary := &mockBackend{getErr: fmt.Errorf("keychain unavailable"), setErr: fmt.Errorf("keychain unavailable")}
	fallback := &mockBackend{}

	key, err := getOrCreateKeyWithBackends(primary, fallback)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(key) != 32 {
		t.Errorf("generated key wrong length: got %d, want 32", len(key))
	}
	if fallback.setCalls != 1 {
		t.Error("should store new key in fallback when primary unavailable")
	}
}

func TestGetOrCreateKeyStoresInPrimary(t *testing.T) {
	primary := &mockBackend{}
	fallback := &mockBackend{}

	key, err := getOrCreateKeyWithBackends(primary, fallback)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(key) != 32 {
		t.Errorf("generated key wrong length: got %d, want 32", len(key))
	}
	if primary.setCalls != 1 {
		t.Error("should store new key in primary when available")
	}
	if fallback.setCalls != 0 {
		t.Error("should not use fallback when primary works")
	}
}

func TestGetOrCreateKeyBothBackendsFail(t *testing.T) {
	primary := &mockBackend{
		getErr: fmt.Errorf("keychain unavailable"),
		setErr: fmt.Errorf("keychain write failed"),
	}
	fallback := &mockBackend{
		getErr: fmt.Errorf("file not found"),
		setErr: fmt.Errorf("permission denied"),
	}

	_, err := getOrCreateKeyWithBackends(primary, fallback)
	if err == nil {
		t.Fatal("expected error when both backends fail to store")
	}

	// Verify error message contains both backend errors
	errMsg := err.Error()
	if !strings.Contains(errMsg, "keychain write failed") {
		t.Errorf("error should mention keychain failure: %v", err)
	}
	if !strings.Contains(errMsg, "permission denied") {
		t.Errorf("error should mention file failure: %v", err)
	}
	if !strings.Contains(errMsg, "Remediation") {
		t.Errorf("error should contain remediation guidance: %v", err)
	}
}

func TestEncodeDecodeKey(t *testing.T) {
	original := make([]byte, 32)
	for i := range original {
		original[i] = byte(i)
	}

	encoded := encodeKey(original)
	decoded, err := decodeKey(encoded)
	if err != nil {
		t.Fatalf("decodeKey failed: %v", err)
	}

	if !bytes.Equal(original, decoded) {
		t.Errorf("round-trip failed: got %v, want %v", decoded, original)
	}
}

func TestDecodeKeyInvalidBase64(t *testing.T) {
	_, err := decodeKey("not-valid-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestDecodeKeyWrongLength(t *testing.T) {
	encoded := encodeKey([]byte("too-short"))
	_, err := decodeKey(encoded)
	if err == nil {
		t.Error("expected error for wrong key length")
	}
}

func TestKeychainBackend(t *testing.T) {
	backend := &keychainBackend{}

	// Generate a test key
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i * 2)
	}

	// Store it
	if err := backend.Set(key); err != nil {
		// Skip if keychain unavailable (CI environment)
		t.Skipf("keychain unavailable: %v", err)
	}

	// Retrieve it
	retrieved, err := backend.Get()
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if !bytes.Equal(key, retrieved) {
		t.Errorf("retrieved key doesn't match: got %v, want %v", retrieved, key)
	}

	// Clean up
	_ = backend.Delete()
}

func TestFileBackend(t *testing.T) {
	// Use temp directory
	tmpDir := t.TempDir()
	backend := &fileBackend{path: filepath.Join(tmpDir, "test.key")}

	// Generate a test key
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i * 3)
	}

	// Store it
	if err := backend.Set(key); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Verify file permissions
	info, err := os.Stat(backend.path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("wrong permissions: got %o, want 0600", info.Mode().Perm())
	}

	// Retrieve it
	retrieved, err := backend.Get()
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if !bytes.Equal(key, retrieved) {
		t.Errorf("retrieved key doesn't match: got %v, want %v", retrieved, key)
	}

	// Delete it
	if err := backend.Delete(); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify deleted
	if _, err := os.Stat(backend.path); !os.IsNotExist(err) {
		t.Error("file should be deleted")
	}
}

func TestFileBackendNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	backend := &fileBackend{path: filepath.Join(tmpDir, "nonexistent.key")}

	_, err := backend.Get()
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestFileBackendTrimsWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "test.key")
	backend := &fileBackend{path: keyPath}

	// Generate a test key
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i * 5)
	}

	// Store it normally first
	if err := backend.Set(key); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Manually add trailing newlines to simulate editor modification
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := os.WriteFile(keyPath, append(data, '\n', '\n', ' ', '\n'), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Should still read correctly with whitespace trimmed
	retrieved, err := backend.Get()
	if err != nil {
		t.Fatalf("Get failed after adding whitespace: %v", err)
	}

	if !bytes.Equal(key, retrieved) {
		t.Error("key should be retrieved correctly despite trailing whitespace")
	}
}

func TestDefaultKeyFilePath(t *testing.T) {
	path := DefaultKeyFilePath()

	// Path should not be empty
	if path == "" {
		t.Error("DefaultKeyFilePath returned empty string")
	}

	// Path should end with the expected filename
	if filepath.Base(path) != "encryption.key" {
		t.Errorf("path should end with encryption.key, got %s", filepath.Base(path))
	}

	// Path should contain .moat directory
	dir := filepath.Dir(path)
	if filepath.Base(dir) != ".moat" {
		t.Errorf("path should be in .moat directory, got %s", dir)
	}
}

func TestDeleteKey(t *testing.T) {
	// Create a key first
	_, err := GetOrCreateKey()
	if err != nil {
		t.Fatalf("GetOrCreateKey: %v", err)
	}

	// Delete should succeed (deletes from wherever it was stored)
	if err := DeleteKey(); err != nil {
		t.Errorf("DeleteKey: %v", err)
	}

	// Calling delete again should also succeed (idempotent)
	if err := DeleteKey(); err != nil {
		t.Errorf("DeleteKey (second call): %v", err)
	}
}
