//go:build !windows

package githubscope

import (
	"os"
	"syscall"
)

// noFollow makes the store's temp-file open fail on a symlink instead of
// following it.
const noFollow = syscall.O_NOFOLLOW

const discoveryNonblock = syscall.O_NONBLOCK

// lockFile takes an exclusive advisory lock on the store's lock file so
// concurrent moat processes serialize their read-modify-write cycles. Without
// it, two runs approving repositories at once could each write back their own
// copy and drop the other's approvals.
func lockFile(f *os.File) (unlock func(), err error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}, nil
}

// Sync the directory entry as well as the file contents before acknowledging
// a write, so a power loss cannot silently lose the latest store replacement.
func syncStoreDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
