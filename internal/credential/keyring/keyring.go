// Package keyring provides secure storage for the credential encryption key.
package keyring

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

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
	data, err := os.ReadFile(f.path)
	if err != nil {
		return nil, fmt.Errorf("reading key file: %w", err)
	}
	return decodeKey(string(data))
}

func (f *fileBackend) Set(key []byte) error {
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating key directory: %w", err)
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
