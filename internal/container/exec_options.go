package container

import "io"

// TTYSize is a terminal dimension update for an interactive exec session.
type TTYSize struct {
	Width  uint
	Height uint
}

// ExecOptions configures an interactive (PTY-backed) exec session.
// It mirrors AttachOptions but targets an exec'd process rather than the
// container's main process, and carries a Resize channel so the caller can
// propagate SIGWINCH to this specific exec session (a container may have many).
type ExecOptions struct {
	Stdin  io.Reader // forwarded to the exec'd process
	Stdout io.Writer // receives the exec'd process output
	Stderr io.Writer // receives stderr (ignored in TTY mode, where it merges into Stdout)
	TTY    bool      // allocate a PTY (raw terminal) for the exec

	// InitialWidth/InitialHeight size the PTY before the process queries it.
	InitialWidth  uint
	InitialHeight uint

	// Resize, if non-nil, delivers terminal size changes for this exec session.
	// The runtime applies each value until the channel is closed or the exec ends.
	Resize <-chan TTYSize
}
