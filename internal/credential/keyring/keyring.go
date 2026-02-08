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
// Concurrency: All key creation operations are protected by a global file lock
// (~/.moat/key.lock) to prevent race conditions when multiple processes attempt
// to create a key simultaneously. Both keychain and file backends check for
// existing keys before writing to avoid overwriting keys created by other processes.
// On Windows, file locking is a no-op, but Windows Credential Manager is the primary
// backend and concurrent first-run scenarios in file fallback are rare.
//
// Security: The file backend will refuse to read keys from files with overly
// permissive permissions (anything other than 0600). If permissions have been
// changed, the key may have been compromised and should be rotated.
package keyring

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	// ServiceName is the default keyring service identifier.
	// Can be overridden with MOAT_KEYRING_SERVICE environment variable for test isolation.
	ServiceName = "moat"
	// AccountName is the keyring account identifier.
	AccountName = "encryption-key"
	// KeySize is the required encryption key size in bytes.
	KeySize = 32
)

// getServiceName returns the keyring service name, checking environment variable first.
// This allows tests to use isolated keyring entries via MOAT_KEYRING_SERVICE=moat-test.
func getServiceName() string {
	if name := os.Getenv("MOAT_KEYRING_SERVICE"); name != "" {
		return name
	}
	return ServiceName
}

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
	encoded, err := keyring.Get(getServiceName(), AccountName)
	if err != nil {
		return nil, fmt.Errorf("keychain get: %w", err)
	}
	return decodeKey(encoded)
}

func (k *keychainBackend) Set(key []byte) error {
	// Check if key already exists - don't overwrite to prevent race conditions.
	// If another process created a key between our Get() and Set() calls,
	// we should use that key instead of overwriting it.
	serviceName := getServiceName()
	if _, err := keyring.Get(serviceName, AccountName); err == nil {
		return nil // Key already exists, don't overwrite
	}

	encoded := encodeKey(key)
	if err := keyring.Set(serviceName, AccountName, encoded); err != nil {
		return fmt.Errorf("keychain set: %w", err)
	}
	return nil
}

func (k *keychainBackend) Delete() error {
	if err := keyring.Delete(getServiceName(), AccountName); err != nil {
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

// ErrInsecurePermissions is returned when the key file has overly permissive permissions.
var ErrInsecurePermissions = errors.New("key file has insecure permissions")

func (f *fileBackend) Get() ([]byte, error) {
	// Check file permissions before reading - fail if too permissive.
	// If permissions were changed, the key may have been compromised.
	info, err := os.Stat(f.path)
	if err != nil {
		return nil, fmt.Errorf("reading key file: %w", err)
	}
	perm := info.Mode().Perm()
	if perm&0077 != 0 {
		return nil, fmt.Errorf("%w: %s has permissions %04o (expected 0600).\n"+
			"  The key may have been exposed. To fix:\n"+
			"  1. chmod 600 %s\n"+
			"  2. Consider re-granting credentials: moat grant <provider>",
			ErrInsecurePermissions, f.path, perm, f.path)
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

// ErrNoHomeDirectory is returned when the home directory cannot be determined.
var ErrNoHomeDirectory = errors.New("could not determine home directory for secure key storage")

// DefaultKeyFilePath returns the default path for the fallback key file.
// The path is always absolute to ensure consistent key storage across
// different working directories.
// The service name (from MOAT_KEYRING_SERVICE or default "moat") is included
// in the filename to support test isolation.
// Returns an error if the home directory cannot be determined, as using
// temp directories is insecure (may be world-readable or cleared on reboot).
func DefaultKeyFilePath() (string, error) {
	// Use service-name-based filename only when MOAT_KEYRING_SERVICE is set (test isolation).
	// Otherwise use the standard "encryption.key" filename.
	filename := "encryption.key"
	if name := os.Getenv("MOAT_KEYRING_SERVICE"); name != "" {
		filename = name + ".key"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		// UserHomeDir failed - try $HOME directly (Unix).
		if envHome := os.Getenv("HOME"); envHome != "" {
			return filepath.Join(envHome, ".moat", filename), nil
		}
		// Cannot determine home directory - fail rather than use insecure temp directory.
		// Temp directories may be world-readable, shared between users, or cleared on reboot.
		return "", fmt.Errorf("%w: set $HOME environment variable or ensure user home is configured", ErrNoHomeDirectory)
	}
	return filepath.Join(home, ".moat", filename), nil
}

// generateKey creates a new random encryption key.
func generateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generating random key: %w", err)
	}
	return key, nil
}

// globalLockPath returns the path for the global key operation lock file.
// This lock is used to serialize all key creation operations across both
// keychain and file backends, preventing race conditions.
func globalLockPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		if envHome := os.Getenv("HOME"); envHome != "" {
			home = envHome
		} else {
			home = os.TempDir()
		}
	}
	return filepath.Join(home, ".moat", "key.lock")
}

// withGlobalKeyLock executes fn while holding the global key lock.
// This ensures that only one process at a time can create or modify the encryption key,
// preventing race conditions between keychain and file backend operations.
func withGlobalKeyLock(fn func() ([]byte, error)) ([]byte, error) {
	lockPath := globalLockPath()

	// Ensure lock directory exists
	if err := os.MkdirAll(filepath.Dir(lockPath), 0700); err != nil {
		return nil, fmt.Errorf("creating lock directory: %w", err)
	}

	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("creating global key lock file: %w", err)
	}
	defer lf.Close()

	unlock, err := lockFile(lf)
	if err != nil {
		return nil, fmt.Errorf("acquiring global key lock: %w", err)
	}
	defer unlock()

	return fn()
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
		// Re-read from primary for consistency with fallback path.
		// This ensures we always return the actually stored key.
		storedKey, getErr := primary.Get()
		if getErr != nil {
			return nil, fmt.Errorf("failed to verify stored encryption key in %s: %w", primary.Name(), getErr)
		}
		return storedKey, nil
	}

	// 5. Fall back to file storage
	slog.Info("system keychain unavailable, using file-based key storage",
		"fallback", fallback.Name())
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
// The entire operation is protected by a global file lock to prevent race conditions
// when multiple processes attempt to create a key simultaneously.
func GetOrCreateKey() ([]byte, error) {
	return withGlobalKeyLock(func() ([]byte, error) {
		keyFilePath, err := DefaultKeyFilePath()
		if err != nil {
			return nil, err
		}
		primary := &keychainBackend{}
		fallback := &fileBackend{path: keyFilePath}
		return getOrCreateKeyWithBackends(primary, fallback)
	})
}

// DeleteKey removes the encryption key from all storage backends.
// This is useful for testing cleanup and reset scenarios.
func DeleteKey() error {
	keyFilePath, err := DefaultKeyFilePath()
	if err != nil {
		// If we can't determine the key file path, we can still try to delete from keychain.
		// Log the error but continue with keychain-only deletion.
		slog.Debug("could not determine key file path for deletion", "error", err)
		keyFilePath = "" // Will cause file backend to fail gracefully
	}
	primary := &keychainBackend{}
	fallback := &fileBackend{path: keyFilePath}

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
		return fmt.Errorf("deleting key from all backends: %w",
			errors.Join(
				fmt.Errorf("keychain: %w", primaryErr),
				fmt.Errorf("file: %w", fallbackErr),
			))
	}
	return nil
}
