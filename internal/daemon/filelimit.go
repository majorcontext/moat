//go:build unix

package daemon

import "golang.org/x/sys/unix"

// desiredNofile is the soft RLIMIT_NOFILE the proxy daemon targets at startup.
//
// The daemon is the single shared egress proxy for every active run, so a burst
// of concurrent connections from one container must not exhaust its file
// descriptors and stall every other run's traffic. A package install is the
// common trigger: `bun install` opens up to 64 parallel connections (Bun's
// default since v1.1.33), each consuming FDs on both the client and upstream
// sides, and several runs can install at once. The frequent 1024 soft default
// is easily exhausted by that; 65536 gives ample headroom.
const desiredNofile = 65536

// RaiseFileLimit raises this process's RLIMIT_NOFILE soft limit toward
// desiredNofile, capped at the hard limit. It returns the previous and new soft
// limits. It is a no-op (returns equal values) when the soft limit is already
// at or above the target.
//
// Best-effort: callers treat a returned error as non-fatal (the daemon still
// runs, just with the inherited limit). The step-down fallback handles macOS,
// which rejects a soft limit above kern.maxfilesperproc even when the hard
// limit is nominally higher.
func RaiseFileLimit() (oldSoft, newSoft uint64, err error) {
	var lim unix.Rlimit
	if err = unix.Getrlimit(unix.RLIMIT_NOFILE, &lim); err != nil {
		return 0, 0, err
	}
	oldSoft = lim.Cur

	target := uint64(desiredNofile)
	if lim.Max < target { // the hard limit caps the soft limit
		target = lim.Max
	}
	if oldSoft >= target {
		return oldSoft, oldSoft, nil // already sufficient
	}

	lim.Cur = target
	if err = unix.Setrlimit(unix.RLIMIT_NOFILE, &lim); err == nil {
		return oldSoft, target, nil
	}

	// Fallback: some systems (notably macOS) reject a soft limit above
	// kern.maxfilesperproc even when the hard limit is higher. Step the target
	// down until one sticks or we drop back to the old soft limit.
	for t := target / 2; t > oldSoft; t /= 2 {
		lim.Cur = t
		if setErr := unix.Setrlimit(unix.RLIMIT_NOFILE, &lim); setErr == nil {
			return oldSoft, t, nil
		}
	}
	return oldSoft, oldSoft, err
}
