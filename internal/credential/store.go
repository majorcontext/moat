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
	"regexp"

	"github.com/majorcontext/moat/internal/credential/keyring"
	"github.com/majorcontext/moat/internal/log"
)

// validProfileName matches alphanumeric characters, hyphens, and underscores.
var validProfileName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

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
		return nil, fmt.Errorf("decrypting credential for %s: %w\n"+
			"  This may indicate the encryption key has changed.\n"+
			"  If you recently upgraded moat, your credentials may have been encrypted with the old key.\n"+
			"  To re-authenticate: moat grant %s", provider, err, provider)
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
			log.Debug("Skipping credential", "provider", provider, "error", err)
			continue // Skip unreadable credentials
		}
		creds = append(creds, *cred)
	}

	return creds, nil
}

// ActiveProfile is the credential profile to use. When set, credentials are
// stored in a profile-specific subdirectory (~/.moat/credentials/profiles/<name>/).
// Set via --profile flag or MOAT_PROFILE environment variable.
// Empty string means use the default (unscoped) credential store.
// Not safe for concurrent use; set once during CLI initialization.
var ActiveProfile string

// DefaultStoreDir returns the credential store directory for the active profile.
// When ActiveProfile is set, returns ~/.moat/credentials/profiles/<name>/.
// Otherwise returns the default ~/.moat/credentials/.
func DefaultStoreDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to temp directory to avoid dumping credentials in the working directory.
		log.Warn("could not determine home directory, using temp directory for credentials", "error", err)
		home = os.TempDir()
	}
	base := filepath.Join(home, ".moat", "credentials")
	if ActiveProfile != "" {
		return filepath.Join(base, "profiles", ActiveProfile)
	}
	return base
}

// ValidateProfile checks if a profile name is valid.
// Profile names must start with an alphanumeric character and contain only
// alphanumeric characters, hyphens, and underscores.
func ValidateProfile(name string) error {
	if name == "" {
		return nil
	}
	if !validProfileName.MatchString(name) {
		return fmt.Errorf("invalid profile name %q: must start with a letter or digit and contain only letters, digits, hyphens, and underscores", name)
	}
	return nil
}

// ListProfiles returns the names of all credential profiles.
// Does not include the default (unscoped) profile.
func ListProfiles() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Warn("cannot determine home directory for credential profiles", "error", err)
		return nil, nil
	}
	profilesDir := filepath.Join(home, ".moat", "credentials", "profiles")
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading profiles directory: %w", err)
	}
	var profiles []string
	for _, entry := range entries {
		if entry.IsDir() && ValidateProfile(entry.Name()) == nil {
			profiles = append(profiles, entry.Name())
		}
	}
	return profiles, nil
}

// DefaultEncryptionKey retrieves the encryption key from secure storage.
// Uses system keychain when available, falls back to file-based storage.
func DefaultEncryptionKey() ([]byte, error) {
	return keyring.GetOrCreateKey()
}
