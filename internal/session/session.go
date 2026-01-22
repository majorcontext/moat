// Package session provides shared session management for agent runners.
// Sessions track the state of agent runs (Claude Code, Codex, etc.) and
// persist metadata to disk for later retrieval.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

// Session represents an agent session (Claude Code, Codex, etc.).
type Session struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Workspace      string    `json:"workspace"`
	RunID          string    `json:"runId"`
	Grants         []string  `json:"grants"`
	CreatedAt      time.Time `json:"createdAt"`
	LastAccessedAt time.Time `json:"lastAccessedAt"`
	State          string    `json:"state"` // "running", "stopped", "completed"
}

// State constants for session lifecycle.
const (
	StateRunning   = "running"
	StateStopped   = "stopped"
	StateCompleted = "completed"
)

// validSessionID matches safe session IDs (alphanumeric with hyphens).
var validSessionID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

// Manager handles session persistence and lookup.
type Manager struct {
	dir string
	mu  sync.RWMutex // protects file operations
}

// NewManager creates a session manager for the given directory.
func NewManager(dir string) *Manager {
	return &Manager{dir: dir}
}

// Dir returns the session storage directory.
func (m *Manager) Dir() string {
	return m.dir
}

// Create creates a new session.
func (m *Manager) Create(workspace, runID, name string, grants []string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session := &Session{
		ID:             runID, // Use run ID as session ID for simplicity
		Name:           name,
		Workspace:      workspace,
		RunID:          runID,
		Grants:         grants,
		CreatedAt:      time.Now(),
		LastAccessedAt: time.Now(),
		State:          StateRunning,
	}

	if err := m.saveLocked(session); err != nil {
		return nil, err
	}

	return session, nil
}

// Get retrieves a session by ID or name.
func (m *Manager) Get(idOrName string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions, err := m.listLocked()
	if err != nil {
		return nil, err
	}

	for _, s := range sessions {
		if s.ID == idOrName || s.Name == idOrName {
			return s, nil
		}
	}

	return nil, fmt.Errorf("session not found: %s", idOrName)
}

// GetByWorkspace returns the most recent session for a workspace.
func (m *Manager) GetByWorkspace(workspace string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions, err := m.listLocked()
	if err != nil {
		return nil, err
	}

	var match *Session
	for _, s := range sessions {
		if s.Workspace == workspace {
			if match == nil || s.LastAccessedAt.After(match.LastAccessedAt) {
				match = s
			}
		}
	}

	if match == nil {
		return nil, fmt.Errorf("no session found for workspace: %s", workspace)
	}

	return match, nil
}

// List returns all sessions, sorted by last accessed time (most recent first).
func (m *Manager) List() ([]*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.listLocked()
}

// listLocked is the internal implementation of List that assumes the lock is held.
func (m *Manager) listLocked() ([]*Session, error) {
	if err := os.MkdirAll(m.dir, 0755); err != nil {
		return nil, fmt.Errorf("creating sessions directory: %w", err)
	}

	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, fmt.Errorf("reading sessions directory: %w", err)
	}

	sessions := make([]*Session, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		session, err := m.loadLocked(entry.Name())
		if err != nil {
			continue // Skip corrupted sessions
		}
		sessions = append(sessions, session)
	}

	// Sort by last accessed time, most recent first
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].LastAccessedAt.After(sessions[j].LastAccessedAt)
	})

	return sessions, nil
}

// UpdateState updates the state of a session.
func (m *Manager) UpdateState(id, state string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, err := m.loadLocked(id)
	if err != nil {
		return err
	}

	session.State = state
	session.LastAccessedAt = time.Now()

	return m.saveLocked(session)
}

// Touch updates the last accessed time of a session.
func (m *Manager) Touch(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, err := m.loadLocked(id)
	if err != nil {
		return err
	}

	session.LastAccessedAt = time.Now()
	return m.saveLocked(session)
}

// Delete removes a session.
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !validSessionID.MatchString(id) {
		return fmt.Errorf("invalid session ID: %s", id)
	}
	sessionDir := filepath.Join(m.dir, id)
	return os.RemoveAll(sessionDir)
}

// save persists a session to disk. It acquires a write lock.
func (m *Manager) save(session *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked(session)
}

// saveLocked persists a session to disk using atomic write-rename pattern.
// This prevents corruption from concurrent writes or crashes during write.
// Caller must hold m.mu.
func (m *Manager) saveLocked(session *Session) error {
	if !validSessionID.MatchString(session.ID) {
		return fmt.Errorf("invalid session ID: %s", session.ID)
	}
	sessionDir := filepath.Join(m.dir, session.ID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return fmt.Errorf("creating session directory: %w", err)
	}

	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling session: %w", err)
	}

	metadataPath := filepath.Join(sessionDir, "metadata.json")

	// Write to a temporary file first, then atomically rename.
	// This ensures we never have a partial or corrupted metadata file.
	tmpPath := metadataPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("writing session metadata: %w", err)
	}

	// Atomically rename temp file to final path
	if err := os.Rename(tmpPath, metadataPath); err != nil {
		// Clean up temp file on failure
		_ = os.Remove(tmpPath)
		return fmt.Errorf("renaming session metadata: %w", err)
	}

	return nil
}

// load reads a session from disk. It acquires a read lock.
func (m *Manager) load(id string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loadLocked(id)
}

// loadLocked reads a session from disk. Caller must hold m.mu.
func (m *Manager) loadLocked(id string) (*Session, error) {
	if !validSessionID.MatchString(id) {
		return nil, fmt.Errorf("invalid session ID: %s", id)
	}
	metadataPath := filepath.Join(m.dir, id, "metadata.json")
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("reading session metadata: %w", err)
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("parsing session metadata: %w", err)
	}

	return &session, nil
}

// CleanupOldSessions removes sessions older than the given duration.
func (m *Manager) CleanupOldSessions(maxAge time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sessions, err := m.listLocked()
	if err != nil {
		return err
	}

	cutoff := time.Now().Add(-maxAge)
	for _, s := range sessions {
		if s.LastAccessedAt.Before(cutoff) && s.State != StateRunning {
			if !validSessionID.MatchString(s.ID) {
				continue
			}
			sessionDir := filepath.Join(m.dir, s.ID)
			if err := os.RemoveAll(sessionDir); err != nil {
				// Log but continue
				continue
			}
		}
	}

	return nil
}
