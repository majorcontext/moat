//go:build !windows

package term

import (
	"os"

	"golang.org/x/term"
)

// RawModeState holds the previous terminal state for restoration.
type RawModeState struct {
	fd       int
	oldState *term.State
}

// EnableRawMode puts the terminal into raw mode, disabling echo and line buffering.
// Returns a state that must be passed to RestoreTerminal when done.
// This is required for escape sequence detection to work properly.
func EnableRawMode(f *os.File) (*RawModeState, error) {
	fd := int(f.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	return &RawModeState{fd: fd, oldState: oldState}, nil
}

// RestoreTerminal restores the terminal to its previous state.
func RestoreTerminal(state *RawModeState) error {
	if state == nil || state.oldState == nil {
		return nil
	}
	return term.Restore(state.fd, state.oldState)
}

// IsTerminal returns true if the file is a terminal.
func IsTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// GetSize returns the terminal dimensions (width, height).
// Returns (0, 0) if the file is not a terminal or size cannot be determined.
func GetSize(f *os.File) (width, height int) {
	w, h, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0, 0
	}
	return w, h
}
