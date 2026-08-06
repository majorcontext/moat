//go:build !windows

package routing

import (
	"os"
	"syscall"
)

// lockFile acquires an exclusive advisory lock on the given file, blocking
// until it is available. Returns a function that releases the lock.
func lockFile(f *os.File) (unlock func(), err error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}, nil
}
