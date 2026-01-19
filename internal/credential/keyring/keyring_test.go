package keyring

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

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
