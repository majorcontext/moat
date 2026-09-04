//go:build windows

package serialdev

import "os"

// noFollow is zero on Windows: O_NOFOLLOW does not exist there, and os.OpenFile
// already refuses to follow a directory symlink for writing.
const noFollow = 0

// lockFile on Windows is a no-op: Windows has no flock, and the pin store's
// cross-process race (one process dropping another's pins) is a dev-machine
// inconvenience rather than a security boundary — the attacker model for
// pinning is a device swap, not a concurrent CLI.
func lockFile(_ *os.File) (unlock func(), err error) {
	return func() {}, nil
}
