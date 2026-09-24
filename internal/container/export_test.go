package container

import (
	"sync"
	"testing"
)

// SwapDetectEnv replaces detectEnv (the package's filesystem/constructor test
// seams, see the detectEnviron type in detect.go) for the duration of a test.
// mutate receives the current environment — start from it and override just
// the field(s) a given test cares about — and returns the replacement.
// Restore puts detectEnv back to what it was before the swap; call it via
// t.Cleanup so a failing or early-returning test still restores it.
//
// detectEnv is shared, mutable package state, so a test that calls
// SwapDetectEnv must not also call t.Parallel() — a parallel sibling could
// otherwise observe (or stomp) the swapped values.
//
// Typical use:
//
//	restore := SwapDetectEnv(func(e detectEnviron) detectEnviron {
//		e.rootfulSocket = filepath.Join(t.TempDir(), "podman.sock")
//		return e
//	})
//	t.Cleanup(restore)
func SwapDetectEnv(mutate func(detectEnviron) detectEnviron) (restore func()) {
	prev := detectEnv
	detectEnv = mutate(prev)
	return func() { detectEnv = prev }
}

// resetPodmanGvisorWarnOnce clears the process-global podmanGvisorWarnOnce
// guard so a test can observe warnPodmanGvisorUnverified's output regardless
// of whether an earlier test already consumed it. Without this the warning is
// only ever visible to whichever test happens to run first. Kept as a
// sync.Once reset (rather than folded into detectEnviron) because resetting a
// one-shot guard genuinely needs direct package access, not a swappable seam.
func resetPodmanGvisorWarnOnce(t *testing.T) {
	t.Helper()
	podmanGvisorWarnOnce = sync.Once{}
	t.Cleanup(func() { podmanGvisorWarnOnce = sync.Once{} })
}
