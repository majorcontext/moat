//go:build darwin

package githubscope

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Resolve through the open directory so case aliases share a sticky scope on
// case-insensitive APFS. EvalSymlinks alone preserves the caller's spelling.
func canonicalProjectPath(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var buf [unix.PathMax]byte
	// F_GETPATH takes a path buffer; x/sys has no pointer-buffer fcntl wrapper.
	//nolint:staticcheck // The direct syscall is required for this Darwin-only command.
	_, _, errno := unix.Syscall(unix.SYS_FCNTL, f.Fd(), unix.F_GETPATH, uintptr(unsafe.Pointer(&buf[0])))
	if errno != 0 {
		return "", errno
	}
	return unix.ByteSliceToString(buf[:]), nil
}
