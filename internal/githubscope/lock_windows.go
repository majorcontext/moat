//go:build windows

package githubscope

import (
	"os"

	"golang.org/x/sys/windows"
)

// noFollow is zero on Windows: O_NOFOLLOW does not exist there.
const noFollow = 0

const discoveryNonblock = 0

// Lock one byte at offset zero on every process's shared lock file.
func lockFile(f *os.File) (unlock func(), err error) {
	var position windows.Overlapped
	handle := windows.Handle(f.Fd())
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &position); err != nil {
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(handle, 0, 1, 0, &position) }, nil
}

// Windows does not support syncing a directory through os.File.Sync.
func syncStoreDir(_ string) error { return nil }
