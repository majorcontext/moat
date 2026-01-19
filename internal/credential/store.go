package credential

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// FileStore implements Store using encrypted files.
type FileStore struct {
	dir    string
	cipher cipher.AEAD
}

// NewFileStore creates a file-based credential store.
// key must be 32 bytes for AES-256.
func NewFileStore(dir string, key []byte) (*FileStore, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating credential dir: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}

	return &FileStore{dir: dir, cipher: gcm}, nil
}

func (s *FileStore) path(provider Provider) string {
	return filepath.Join(s.dir, string(provider)+".enc")
}

// Save stores a credential encrypted on disk.
func (s *FileStore) Save(cred Credential) error {
	data, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("marshaling credential: %w", err)
	}

	nonce := make([]byte, s.cipher.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generating nonce: %w", err)
	}

	encrypted := s.cipher.Seal(nonce, nonce, data, nil)
	if err := os.WriteFile(s.path(cred.Provider), encrypted, 0600); err != nil {
		return fmt.Errorf("writing credential file: %w", err)
	}

	return nil
}

// Get retrieves a credential for the given provider.
func (s *FileStore) Get(provider Provider) (*Credential, error) {
	encrypted, err := os.ReadFile(s.path(provider))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("credential not found: %s", provider)
		}
		return nil, fmt.Errorf("reading credential file: %w", err)
	}

	nonceSize := s.cipher.NonceSize()
	if len(encrypted) < nonceSize {
		return nil, fmt.Errorf("invalid credential file")
	}

	nonce, ciphertext := encrypted[:nonceSize], encrypted[nonceSize:]
	data, err := s.cipher.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypting credential: %w", err)
	}

	var cred Credential
	if err := json.Unmarshal(data, &cred); err != nil {
		return nil, fmt.Errorf("unmarshaling credential: %w", err)
	}

	return &cred, nil
}

// Delete removes a credential for the given provider.
func (s *FileStore) Delete(provider Provider) error {
	if err := os.Remove(s.path(provider)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting credential: %w", err)
	}
	return nil
}

// List returns all stored credentials.
func (s *FileStore) List() ([]Credential, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("reading credential dir: %w", err)
	}

	creds := make([]Credential, 0, len(entries))
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".enc" {
			continue
		}
		provider := Provider(entry.Name()[:len(entry.Name())-4])
		cred, err := s.Get(provider)
		if err != nil {
			continue // Skip unreadable credentials
		}
		creds = append(creds, *cred)
	}

	return creds, nil
}

// DefaultStoreDir returns the default credential store directory.
func DefaultStoreDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to current directory if home is unavailable
		return filepath.Join(".", ".moat", "credentials")
	}
	return filepath.Join(home, ".moat", "credentials")
}

// DefaultEncryptionKey returns a key derived from the user's environment.
// In production, this should use a proper key derivation or keychain.
func DefaultEncryptionKey() []byte {
	// For now, use a fixed key. TODO: Use system keychain.
	return []byte("moat-default-encryption-key-32b!")
}
