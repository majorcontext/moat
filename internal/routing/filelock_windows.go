//go:build windows

package routing

import "os"

// lockFile on Windows is a no-op: moat's container runtimes (Docker via a Linux
// VM, Apple containers) are not supported there, so concurrent moat processes
// contending for routes.json is not a scenario that arises. The atomic
// temp+rename in save() still prevents torn reads; only the cross-process
// lost-update guard is absent.
func lockFile(_ *os.File) (unlock func(), err error) {
	return func() {}, nil
}
