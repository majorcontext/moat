//go:build windows

package term

import (
	"errors"
	"os"
)

// RawModeState holds the previous terminal state for restoration.
type RawModeState struct{}

// EnableRawMode puts the terminal into raw mode.
// On Windows, this is not yet implemented.
func EnableRawMode(f *os.File) (*RawModeState, error) {
	return nil, errors.New("raw mode not supported on Windows")
}

// RestoreTerminal restores the terminal to its previous state.
func RestoreTerminal(state *RawModeState) error {
	return nil
}

// IsTerminal returns true if the file is a terminal.
func IsTerminal(f *os.File) bool {
	return false
}

// GetSize returns the terminal dimensions (width, height).
// On Windows, this is not yet implemented.
func GetSize(f *os.File) (width, height int) {
	return 0, 0
}
