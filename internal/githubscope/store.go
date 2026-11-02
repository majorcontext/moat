package githubscope

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/majorcontext/moat/internal/log"
)

// StoreFileName is the approval store's file name under the moat home
// directory. It lives outside every workspace so an agent cannot edit it.
const StoreFileName = "github-repos.json"

// Approval records that the user trusted a repository for a project.
type Approval struct {
	ApprovedAt time.Time `json:"approved_at"`
	// Source is where the repository was found when it was approved, e.g.
	// "./keep (origin)", "moat.yaml extra_repos", or "moat github allow".
	Source string `json:"source,omitempty"`
	// FullName, ID, and NodeIDs cache GitHub's identity for the repository so
	// the policy can recognize it by numeric ID (/repositories/{id}) and by
	// GraphQL node ID (repositoryId). They are refreshed on each run when
	// GitHub is reachable.
	FullName string   `json:"full_name,omitempty"`
	ID       int64    `json:"id,omitempty"`
	NodeIDs  []string `json:"node_ids,omitempty"`
}

// Project is the stored state for one project, keyed by its absolute path.
type Project struct {
	// LockedAt is when the project was first run with github.scope:
	// workspace. Once set, later runs keep the scope even if moat.yaml stops
	// asking for it; `moat github forget --all` clears it.
	LockedAt time.Time           `json:"locked_at"`
	Repos    map[string]Approval `json:"repos"`
}

// RepoNames returns the approved repositories, sorted.
func (p *Project) RepoNames() []string {
	if p == nil {
		return nil
	}
	names := make([]string, 0, len(p.Repos))
	for r := range p.Repos {
		names = append(names, r)
	}
	sort.Strings(names)
	return names
}

type storeFile struct {
	Version  int                 `json:"version"`
	Projects map[string]*Project `json:"projects"`
}

// Store persists per-project approvals as JSON. It is safe for concurrent use
// within a process, and across processes via an advisory lock on a sidecar
// lock file.
type Store struct {
	path string
	mu   sync.Mutex
	lf   *os.File
}

// DefaultStorePath returns the store location under moatHome (see
// config.GlobalConfigDir).
func DefaultStorePath(moatHome string) string {
	return filepath.Join(moatHome, StoreFileName)
}

// OpenStore opens the store at path. The file is created on first write.
//
// A malformed file is an error rather than an empty store: silently
// discarding approvals would also discard the project's lock.
func OpenStore(path string) (*Store, error) {
	if _, err := readStoreFile(path); err != nil {
		return nil, err
	}
	s := &Store{path: path}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	lf, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		log.Debug("opening github approval lock file failed; continuing without cross-process lock",
			"path", path+".lock", "err", err)
		return s, nil
	}
	s.lf = lf
	return s, nil
}

// Close releases the lock file.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lf != nil {
		err := s.lf.Close()
		s.lf = nil
		return err
	}
	return nil
}

// Get returns the stored project, or nil when the project has no record.
func (s *Store) Get(project string) (*Project, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	f, err := readStoreFile(s.path)
	if err != nil {
		return nil, err
	}
	return f.Projects[project], nil
}

// Update applies fn to the project's record under the store lock and writes
// the result. fn receives nil when the project has no record; returning nil
// deletes the record.
func (s *Store) Update(project string, fn func(*Project) (*Project, error)) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	f, err := readStoreFile(s.path)
	if err != nil {
		return err
	}
	next, err := fn(f.Projects[project])
	if err != nil {
		return err
	}
	if next == nil {
		delete(f.Projects, project)
	} else {
		if next.Repos == nil {
			next.Repos = map[string]Approval{}
		}
		f.Projects[project] = next
	}
	return s.write(f)
}

func (s *Store) lock() (func(), error) {
	s.mu.Lock()
	if s.lf == nil {
		return s.mu.Unlock, nil
	}
	u, err := lockFile(s.lf)
	if err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("locking GitHub approvals at %s: %w", s.path+".lock", err)
	}
	return func() {
		u()
		s.mu.Unlock()
	}, nil
}

func readStoreFile(path string) (*storeFile, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(data) == 0) {
		return &storeFile{Version: 1, Projects: map[string]*Project{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading GitHub approvals from %s: %w", path, err)
	}
	var f storeFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing GitHub approvals in %s: %w\n"+
			"  Fix the file by hand, or delete it to re-approve repositories from scratch", path, err)
	}
	if f.Projects == nil {
		f.Projects = map[string]*Project{}
	}
	return &f, nil
}

// write replaces the store file atomically (write-then-rename). Any leftover
// temp file is removed first and the new one is created exclusively without
// following symlinks, so a planted link cannot redirect the write.
func (s *Store) write(f *storeFile) error {
	f.Version = 1
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding GitHub approvals: %w", err)
	}
	tmp := s.path + ".tmp"
	if rmErr := os.Remove(tmp); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		return fmt.Errorf("clearing stale temp file %s: %w", tmp, rmErr)
	}
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY|noFollow, 0o600)
	if err != nil {
		return fmt.Errorf("writing GitHub approvals: %w", err)
	}
	if _, err := out.Write(data); err != nil {
		out.Close()
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("writing GitHub approvals: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("writing GitHub approvals: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("replacing GitHub approvals at %s: %w", s.path, err)
	}
	return nil
}
