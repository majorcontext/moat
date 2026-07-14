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
// (and macOS's 256) is easily exhausted by that; 65536 gives ample headroom.
const desiredNofile = 65536

// RaiseFileLimit raises this process's RLIMIT_NOFILE soft limit toward
// desiredNofile, capped at the hard limit. It returns the previous soft limit
// and the soft limit in effect afterward (always >= oldSoft). It is a no-op
// (equal values) when the soft limit is already at or above the target.
//
// A limit that cannot be raised is not an error — the process keeps its
// existing, already-in-effect limit and err stays nil. err is non-nil only when
// the current limit cannot be read. Some systems, notably macOS, reject a soft
// limit above kern.maxfilesperproc even when the hard limit is nominally
// higher; when the full target is rejected, the fallback binary-searches the
// highest value the system will accept between the old soft limit and the
// target.
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

	// Fast path: the full target is usually accepted.
	lim.Cur = target
	if unix.Setrlimit(unix.RLIMIT_NOFILE, &lim) == nil {
		return oldSoft, target, nil
	}

	// Fallback: the target was rejected (e.g. macOS caps the soft limit at
	// kern.maxfilesperproc, below the hard limit). Binary-search the highest
	// value in (oldSoft, target) the system accepts. lo is always known-good
	// (oldSoft is currently in effect), hi is always known-bad (a rejected
	// value). Each successful Setrlimit leaves the process at that value, so
	// once the search narrows, the process's soft limit equals best.
	best := oldSoft
	lo, hi := oldSoft, target
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		lim.Cur = mid
		if unix.Setrlimit(unix.RLIMIT_NOFILE, &lim) == nil {
			best, lo = mid, mid
		} else {
			hi = mid
		}
	}
	return oldSoft, best, nil
}
