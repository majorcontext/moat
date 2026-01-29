package codex

import (
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/session"
)

// Session type aliases for backward compatibility.
// New code should use session.Session directly.
type Session = session.Session

// SessionState constants for backward compatibility.
const (
	SessionStateRunning   = session.StateRunning
	SessionStateStopped   = session.StateStopped
	SessionStateCompleted = session.StateCompleted
)

// SessionManager wraps session.Manager for Codex CLI sessions.
type SessionManager struct {
	*session.Manager
}

// NewSessionManager creates a session manager for Codex CLI sessions.
func NewSessionManager() (*SessionManager, error) {
	dir, err := DefaultSessionDir()
	if err != nil {
		return nil, err
	}
	return &SessionManager{Manager: session.NewManager(dir)}, nil
}

// DefaultSessionDir returns the default Codex session storage directory.
func DefaultSessionDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".moat", "codex", "sessions"), nil
}
