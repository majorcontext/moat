//go:build !windows

package serialdev

import (
	"os"
	"syscall"
)

// noFollow makes the pin store's temp-file open fail on a symlink instead of
// following it to whatever the link points at.
const noFollow = syscall.O_NOFOLLOW

// lockFile takes an exclusive advisory lock on the pin store's lock file,
// serializing read-modify-write cycles across processes. Two moat processes
// approving devices concurrently would otherwise race: each reads the file,
// each writes its own map back, and the second write drops the first's pins —
// silently re-arming trust-on-first-use for every device the loser had pinned.
func lockFile(f *os.File) (unlock func(), err error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}, nil
}
