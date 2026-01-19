//go:build !windows

package keyring

import (
	"os"
	"syscall"
)

// lockFile acquires an exclusive lock on the given file.
// Returns a function to release the lock.
func lockFile(f *os.File) (unlock func(), err error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}, nil
}
