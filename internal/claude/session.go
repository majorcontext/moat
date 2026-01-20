package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Session represents a Claude Code session.
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

// SessionState constants
const (
	SessionStateRunning   = "running"
	SessionStateStopped   = "stopped"
	SessionStateCompleted = "completed"
)

// SessionManager handles session persistence and lookup.
type SessionManager struct {
	dir string
}

// NewSessionManager creates a session manager using the default directory.
func NewSessionManager() (*SessionManager, error) {
	dir, err := DefaultSessionDir()
	if err != nil {
		return nil, err
	}
	return &SessionManager{dir: dir}, nil
}

// DefaultSessionDir returns the default session storage directory.
func DefaultSessionDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".moat", "claude", "sessions"), nil
}

// Create creates a new session.
func (m *SessionManager) Create(workspace, runID, name string, grants []string) (*Session, error) {
	session := &Session{
		ID:             runID, // Use run ID as session ID for simplicity
		Name:           name,
		Workspace:      workspace,
		RunID:          runID,
		Grants:         grants,
		CreatedAt:      time.Now(),
		LastAccessedAt: time.Now(),
		State:          SessionStateRunning,
	}

	if err := m.save(session); err != nil {
		return nil, err
	}

	return session, nil
}

// Get retrieves a session by ID or name.
func (m *SessionManager) Get(idOrName string) (*Session, error) {
	sessions, err := m.List()
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
func (m *SessionManager) GetByWorkspace(workspace string) (*Session, error) {
	sessions, err := m.List()
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
func (m *SessionManager) List() ([]*Session, error) {
	if err := os.MkdirAll(m.dir, 0755); err != nil {
		return nil, fmt.Errorf("creating sessions directory: %w", err)
	}

	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, fmt.Errorf("reading sessions directory: %w", err)
	}

	var sessions []*Session
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		session, err := m.load(entry.Name())
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
func (m *SessionManager) UpdateState(id, state string) error {
	session, err := m.load(id)
	if err != nil {
		return err
	}

	session.State = state
	session.LastAccessedAt = time.Now()

	return m.save(session)
}

// Touch updates the last accessed time of a session.
func (m *SessionManager) Touch(id string) error {
	session, err := m.load(id)
	if err != nil {
		return err
	}

	session.LastAccessedAt = time.Now()
	return m.save(session)
}

// Delete removes a session.
func (m *SessionManager) Delete(id string) error {
	sessionDir := filepath.Join(m.dir, id)
	return os.RemoveAll(sessionDir)
}

// sessionPath returns the path to a session's metadata file.
func (m *SessionManager) sessionPath(id string) string {
	return filepath.Join(m.dir, id, "metadata.json")
}

// save persists a session to disk.
func (m *SessionManager) save(session *Session) error {
	sessionDir := filepath.Join(m.dir, session.ID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return fmt.Errorf("creating session directory: %w", err)
	}

	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling session: %w", err)
	}

	metadataPath := filepath.Join(sessionDir, "metadata.json")
	if err := os.WriteFile(metadataPath, data, 0644); err != nil {
		return fmt.Errorf("writing session metadata: %w", err)
	}

	return nil
}

// load reads a session from disk.
func (m *SessionManager) load(id string) (*Session, error) {
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
func (m *SessionManager) CleanupOldSessions(maxAge time.Duration) error {
	sessions, err := m.List()
	if err != nil {
		return err
	}

	cutoff := time.Now().Add(-maxAge)
	for _, s := range sessions {
		if s.LastAccessedAt.Before(cutoff) && s.State != SessionStateRunning {
			if err := m.Delete(s.ID); err != nil {
				// Log but continue
				continue
			}
		}
	}

	return nil
}
