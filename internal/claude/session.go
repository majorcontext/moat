package claude

import (
	"os"
	"path/filepath"

	"github.com/andybons/moat/internal/session"
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

// SessionManager wraps session.Manager for Claude Code sessions.
type SessionManager struct {
	*session.Manager
}

// NewSessionManager creates a session manager for Claude Code sessions.
func NewSessionManager() (*SessionManager, error) {
	dir, err := DefaultSessionDir()
	if err != nil {
		return nil, err
	}
	return &SessionManager{Manager: session.NewManager(dir)}, nil
}

// DefaultSessionDir returns the default Claude session storage directory.
func DefaultSessionDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".moat", "claude", "sessions"), nil
}
