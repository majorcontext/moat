// Package keyring provides secure storage for the credential encryption key.
//
// Platform requirements:
//   - macOS: Uses Keychain via Security framework (works out of the box)
//   - Linux: Requires libsecret (GNOME), kwallet (KDE), or pass (CLI)
//   - Windows: Uses Windows Credential Manager (works out of the box)
//   - Headless/CI: Automatically falls back to file-based storage at ~/.moat/encryption.key
//
// The package attempts to store keys in the system keychain first for better security.
// If the keychain is unavailable (e.g., in CI, headless servers, or containers),
// it silently falls back to file-based storage with restricted permissions (0600).
//
// Concurrency: File-based storage uses file locking (flock on Unix) to prevent
// race conditions during concurrent key creation. On Windows, the file backend
// does not use locking since Windows Credential Manager is the primary backend
// and file fallback is mainly used in headless/CI environments where concurrent
// first-run scenarios are rare. If concurrent access to file storage is needed
// on Windows, external synchronization should be used.
package keyring

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	// ServiceName is the keyring service identifier.
	ServiceName = "moat"
	// AccountName is the keyring account identifier.
	AccountName = "encryption-key"
	// KeySize is the required encryption key size in bytes.
	KeySize = 32
)

// encodeKey converts a raw key to base64 for keychain storage.
func encodeKey(key []byte) string {
	return base64.StdEncoding.EncodeToString(key)
}

// decodeKey converts a base64-encoded key back to raw bytes.
func decodeKey(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid key encoding: %w", err)
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("invalid key length: expected %d bytes, got %d", KeySize, len(key))
	}
	return key, nil
}

// Backend defines the interface for key storage.
type Backend interface {
	Get() ([]byte, error)
	Set(key []byte) error
	Delete() error
	Name() string
}

// keychainBackend stores keys in the system keychain.
type keychainBackend struct{}

func (k *keychainBackend) Get() ([]byte, error) {
	encoded, err := keyring.Get(ServiceName, AccountName)
	if err != nil {
		return nil, fmt.Errorf("keychain get: %w", err)
	}
	return decodeKey(encoded)
}

func (k *keychainBackend) Set(key []byte) error {
	encoded := encodeKey(key)
	if err := keyring.Set(ServiceName, AccountName, encoded); err != nil {
		return fmt.Errorf("keychain set: %w", err)
	}
	return nil
}

func (k *keychainBackend) Delete() error {
	if err := keyring.Delete(ServiceName, AccountName); err != nil {
		return fmt.Errorf("keychain delete: %w", err)
	}
	return nil
}

func (k *keychainBackend) Name() string {
	return "system keychain"
}

// fileBackend stores keys in a file with restricted permissions.
type fileBackend struct {
	path string
}

func (f *fileBackend) Get() ([]byte, error) {
	// Check file permissions before reading - warn if too permissive
	info, err := os.Stat(f.path)
	if err != nil {
		return nil, fmt.Errorf("reading key file: %w", err)
	}
	perm := info.Mode().Perm()
	if perm&0077 != 0 {
		slog.Warn("key file has permissive permissions, should be 0600",
			"path", f.path,
			"permissions", fmt.Sprintf("%04o", perm))
	}

	data, err := os.ReadFile(f.path)
	if err != nil {
		return nil, fmt.Errorf("reading key file: %w", err)
	}
	// Trim whitespace to handle trailing newlines from manual editing
	return decodeKey(strings.TrimSpace(string(data)))
}

func (f *fileBackend) Set(key []byte) error {
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating key directory: %w", err)
	}

	// Use a lock file to prevent race conditions when multiple processes
	// try to create the key simultaneously. The lock file is cleaned up on
	// normal exit via defer. If a process crashes while holding the lock,
	// the stale .lock file is harmless and can be safely deleted manually.
	// The next process will create a new lock file.
	lockPath := f.path + ".lock"
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("creating lock file: %w", err)
	}
	defer lf.Close()
	defer os.Remove(lockPath)

	// Acquire exclusive lock
	unlock, err := lockFile(lf)
	if err != nil {
		return fmt.Errorf("acquiring lock: %w", err)
	}
	defer unlock()

	// If key already exists, don't overwrite it - another process may have created it
	// while we waited for the lock. The caller should re-read the key after this returns.
	if _, err := os.Stat(f.path); err == nil {
		return nil
	}

	encoded := encodeKey(key)
	if err := os.WriteFile(f.path, []byte(encoded), 0600); err != nil {
		return fmt.Errorf("writing key file: %w", err)
	}
	return nil
}

func (f *fileBackend) Delete() error {
	if err := os.Remove(f.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting key file: %w", err)
	}
	return nil
}

func (f *fileBackend) Name() string {
	return "file (" + f.path + ")"
}

// DefaultKeyFilePath returns the default path for the fallback key file.
func DefaultKeyFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".moat", "encryption.key")
	}
	return filepath.Join(home, ".moat", "encryption.key")
}

// generateKey creates a new random encryption key.
func generateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generating random key: %w", err)
	}
	return key, nil
}

// getOrCreateKeyWithBackends retrieves or creates an encryption key using the provided backends.
func getOrCreateKeyWithBackends(primary, fallback Backend) ([]byte, error) {
	// 1. Try primary backend (keychain)
	if key, err := primary.Get(); err == nil {
		return key, nil
	}

	// 2. Try fallback backend (file)
	if key, err := fallback.Get(); err == nil {
		return key, nil
	}

	// 3. Generate new key
	key, err := generateKey()
	if err != nil {
		return nil, err
	}

	// 4. Try to store in primary
	primaryErr := primary.Set(key)
	if primaryErr == nil {
		return key, nil
	}

	// 5. Fall back to file storage
	slog.Info("system keychain unavailable, using file-based key storage",
		"fallback_path", DefaultKeyFilePath())
	if fallbackErr := fallback.Set(key); fallbackErr != nil {
		return nil, fmt.Errorf("storing encryption key failed.\n"+
			"  Keychain (%s): %v\n"+
			"  File (%s): %v\n"+
			"Remediation: Ensure ~/.moat directory is writable and check system keychain access settings",
			primary.Name(), primaryErr, fallback.Name(), fallbackErr)
	}

	// Re-read the key from fallback to ensure we return the actual stored key.
	// This is mandatory because another process may have created the key while we waited
	// for the lock, and our generated key may differ from what was stored.
	storedKey, err := fallback.Get()
	if err != nil {
		return nil, fmt.Errorf("failed to verify stored encryption key: %w", err)
	}
	return storedKey, nil
}

// GetOrCreateKey retrieves the encryption key from keychain or file, generating a new one if needed.
func GetOrCreateKey() ([]byte, error) {
	primary := &keychainBackend{}
	fallback := &fileBackend{path: DefaultKeyFilePath()}
	return getOrCreateKeyWithBackends(primary, fallback)
}

// DeleteKey removes the encryption key from all storage backends.
// This is useful for testing cleanup and reset scenarios.
func DeleteKey() error {
	primary := &keychainBackend{}
	fallback := &fileBackend{path: DefaultKeyFilePath()}

	// Try to delete from both backends, collecting any errors
	var primaryErr, fallbackErr error
	primaryErr = primary.Delete()
	fallbackErr = fallback.Delete()

	// Log partial failures for observability
	if primaryErr != nil && fallbackErr == nil {
		slog.Debug("keychain delete failed (file delete succeeded)", "error", primaryErr)
	}
	if fallbackErr != nil && primaryErr == nil {
		slog.Debug("file delete failed (keychain delete succeeded)", "error", fallbackErr)
	}

	// Return error only if both failed (one succeeding is fine)
	if primaryErr != nil && fallbackErr != nil {
		return fmt.Errorf("deleting key: keychain: %v; file: %w", primaryErr, fallbackErr)
	}
	return nil
}
