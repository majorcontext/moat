//go:build unix

package daemon

import (
	"testing"

	"golang.org/x/sys/unix"
)

func currentNofile(t *testing.T) (soft, hard uint64) {
	t.Helper()
	var lim unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &lim); err != nil {
		t.Fatalf("Getrlimit: %v", err)
	}
	return lim.Cur, lim.Max
}

func TestRaiseFileLimit(t *testing.T) {
	beforeSoft, hard := currentNofile(t)

	oldSoft, newSoft, err := RaiseFileLimit()
	if err != nil {
		t.Fatalf("RaiseFileLimit: %v", err)
	}
	if oldSoft != beforeSoft {
		t.Errorf("reported oldSoft %d, want the current soft limit %d", oldSoft, beforeSoft)
	}
	if newSoft < oldSoft {
		t.Errorf("newSoft %d must never be below oldSoft %d (the limit is only ever raised)", newSoft, oldSoft)
	}
	if newSoft > hard {
		t.Errorf("newSoft %d must not exceed the hard limit %d", newSoft, hard)
	}
	// The process's actual soft limit must reflect the reported new value.
	gotSoft, _ := currentNofile(t)
	if gotSoft != newSoft {
		t.Errorf("process soft limit is %d, want the reported newSoft %d", gotSoft, newSoft)
	}
}

// TestRaiseFileLimitNonLowering asserts a second call never reduces the limit —
// it either reaches the target once and no-ops, or (on systems that cap the
// soft limit below the hard limit, e.g. macOS) stays put. A limit that cannot
// be raised further is reported as success, not an error.
func TestRaiseFileLimitNonLowering(t *testing.T) {
	_, firstNew, err := RaiseFileLimit()
	if err != nil {
		t.Fatalf("first RaiseFileLimit: %v", err)
	}

	_, secondNew, err := RaiseFileLimit()
	if err != nil {
		t.Fatalf("second RaiseFileLimit: %v", err)
	}
	if secondNew < firstNew {
		t.Errorf("second call lowered the soft limit: %d -> %d", firstNew, secondNew)
	}
	afterSoft, _ := currentNofile(t)
	if afterSoft < firstNew {
		t.Errorf("process soft limit dropped after a second call: %d -> %d", firstNew, afterSoft)
	}
}
