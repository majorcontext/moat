package credential

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/majorcontext/moat/internal/config"
)

// NewProfileStore opens a profile with its explicitly configured ancestors.
// Save always writes locally; refreshes use LockRefreshStore to retain ownership.
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

// IsExcluded reports whether this store locally excludes a grant from
// inheritance, including future grants added to its parent.
func (s *FileStore) IsExcluded(provider Provider) (bool, error) {
	if err := validateProvider(provider); err != nil {
		return false, err
	}
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
	if blocked, err := s.IsExcluded(provider); err != nil {
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

// LockRefreshStore pins operations to the owning profile and locks that
// credential across the re-read, token exchange, and save. Save and Delete use
// the same lock, so CLI grants and revocations cannot interleave with refresh.
// The caller must release the lock when finished; the returned store must not
// be retained or used after release.
func LockRefreshStore(ctx context.Context, store Store, provider Provider) (Store, func(), error) {
	if s, ok := store.(*FileStore); ok {
		owner, err := s.Owner(provider)
		if err != nil {
			return nil, nil, err
		}
		unlock, err := owner.lock(ctx, provider)
		if err != nil {
			return nil, nil, err
		}
		// Re-read under the lock: a revoke may have completed while we waited.
		if _, err := owner.Get(provider); err != nil {
			unlock()
			return nil, nil, err
		}
		return &lockedRefreshStore{FileStore: owner, provider: provider}, unlock, nil
	}
	return store, func() {}, nil
}

func (s *FileStore) lock(ctx context.Context, provider Provider) (func(), error) {
	if err := validateProvider(provider); err != nil {
		return nil, err
	}
	// The stable lock inode is separate from the atomically replaced secret.
	lock := flock.New(s.path(provider)+".lock", flock.SetPermissions(0o600))
	locked, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil || !locked {
		_ = lock.Close()
		if err == nil {
			err = fmt.Errorf("lock unavailable")
		}
		return nil, fmt.Errorf("locking credential %s: %w", provider, err)
	}
	return func() { _ = lock.Close() }, nil
}

type lockedRefreshStore struct {
	*FileStore
	provider Provider
}

func (s *lockedRefreshStore) Save(cred Credential) error {
	if cred.Provider != s.provider {
		return fmt.Errorf("refresh store is locked for %s, not %s", s.provider, cred.Provider)
	}
	return s.save(cred)
}

func (s *lockedRefreshStore) Delete(provider Provider) error {
	return fmt.Errorf("cannot delete %s through a refresh store", provider)
}
