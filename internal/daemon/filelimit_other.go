//go:build !unix

package daemon

// RaiseFileLimit is a no-op on platforms without POSIX rlimits. The proxy
// daemon runs on the host (macOS or Linux, both unix), so this stub exists only
// to keep the package building on other GOOS values.
func RaiseFileLimit() (oldSoft, newSoft uint64, err error) {
	return 0, 0, nil
}
