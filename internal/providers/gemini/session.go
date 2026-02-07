package gemini

import (
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/session"
)

// Session type aliases for backward compatibility.
type Session = session.Session

// SessionState constants.
const (
	SessionStateRunning   = session.StateRunning
	SessionStateStopped   = session.StateStopped
	SessionStateCompleted = session.StateCompleted
)

// SessionManager wraps session.Manager for Gemini CLI sessions.
type SessionManager struct {
	*session.Manager
}

// NewSessionManager creates a session manager for Gemini CLI sessions.
func NewSessionManager() (*SessionManager, error) {
	dir, err := DefaultSessionDir()
	if err != nil {
		return nil, err
	}
	return &SessionManager{Manager: session.NewManager(dir)}, nil
}

// DefaultSessionDir returns the default Gemini session storage directory.
func DefaultSessionDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".moat", "gemini", "sessions"), nil
}
