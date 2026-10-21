package credential

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/config"
)

// NewProfileStore opens a profile with its explicitly configured ancestors.
// Save always writes locally; refreshes must use RefreshStore to retain ownership.
func NewProfileStore(profile string, key []byte) (*FileStore, error) {
	if err := ValidateProfile(profile); err != nil {
		return nil, err
	}
	cfg, err := config.LoadGlobal()
	if err != nil {
		return nil, fmt.Errorf("loading profiles: %w", err)
	}
	chain, err := cfg.ProfileChain(profile)
	if err != nil {
		return nil, err
	}
	var parent *FileStore
	for i := len(chain) - 1; i >= 0; i-- {
		store, err := NewFileStore(StoreDirForProfile(chain[i]), key)
		if err != nil {
			return nil, err
		}
		store.parent, store.profile = parent, chain[i]
		parent = store
	}
	return parent, nil
}

func (s *FileStore) revokedPath(provider Provider) string {
	return filepath.Join(s.dir, string(provider)+".revoked")
}

func (s *FileStore) isRevoked(provider Provider) (bool, error) {
	_, err := os.Stat(s.revokedPath(provider))
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// Owner returns the physical store supplying a credential. Errors, including
// exclusions and unreadable local credentials, never fall through to a parent.
func (s *FileStore) Owner(provider Provider) (*FileStore, error) {
	if err := validateProvider(provider); err != nil {
		return nil, err
	}
	if blocked, err := s.isRevoked(provider); err != nil {
		return nil, err
	} else if blocked {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, provider)
	}
	_, err := os.Stat(s.path(provider))
	if os.IsNotExist(err) && s.parent != nil {
		return s.parent.Owner(provider)
	}
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, provider)
	}
	if err != nil {
		return nil, err
	}
	// The returned store is local only: refreshing cannot change lookup layers.
	return &FileStore{dir: s.dir, cipher: s.cipher, profile: s.profile}, nil
}

// Source returns the profile supplying the grant; empty means the default store.
func (s *FileStore) Source(provider Provider) (string, error) {
	owner, err := s.Owner(provider)
	if err != nil {
		return "", err
	}
	return owner.profile, nil
}

// RefreshStore pins read/write operations to the credential's owning profile.
// Stores without inheritance (including test stores) already have this behavior.
func RefreshStore(store Store, provider Provider) (Store, error) {
	if s, ok := store.(*FileStore); ok {
		return s.Owner(provider)
	}
	return store, nil
}
