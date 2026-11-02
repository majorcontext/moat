//go:build windows

package githubscope

import "os"

// noFollow is zero on Windows: O_NOFOLLOW does not exist there.
const noFollow = 0

// lockFile is a no-op on Windows, where moat runs single-process.
func lockFile(_ *os.File) (unlock func(), err error) {
	return func() {}, nil
}
