package container

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/majorcontext/moat/internal/ui"
)

// resetPodmanGvisorWarnOnce clears the process-global warn-once guard so a test
// can observe the warning regardless of whether an earlier test already
// consumed it. Without this the warning is only ever visible to whichever test
// happens to run first.
func resetPodmanGvisorWarnOnce(t *testing.T) {
	t.Helper()
	podmanGvisorWarnOnce = sync.Once{}
	t.Cleanup(func() { podmanGvisorWarnOnce = sync.Once{} })
}

// TestWarnPodmanGvisorUnverified pins both halves of the contract: the warning
// says something actionable, and it fires at most once per process no matter
// how many runtimes are constructed.
func TestWarnPodmanGvisorUnverified(t *testing.T) {
	resetPodmanGvisorWarnOnce(t)

	var buf bytes.Buffer
	ui.SetWriter(&buf)
	t.Cleanup(func() { ui.SetWriter(os.Stderr) })

	warnPodmanGvisorUnverified()
	first := buf.String()

	if !strings.Contains(first, "unverified under podman") {
		t.Errorf("warning should explain the engine report is unverified, got: %q", first)
	}
	if !strings.Contains(first, "--no-sandbox") {
		t.Errorf("warning should name the escape hatch, got: %q", first)
	}

	// Companion: subsequent constructions must stay silent.
	warnPodmanGvisorUnverified()
	warnPodmanGvisorUnverified()
	if got := buf.String(); got != first {
		t.Errorf("warning should fire once per process, got repeat output: %q", got)
	}
}

// TestWarnPodmanGvisorUnverifiedResetIsObservable guards the seam itself: if
// resetting stopped working, the test above would silently pass on a reused
// once and stop asserting anything.
func TestWarnPodmanGvisorUnverifiedResetIsObservable(t *testing.T) {
	resetPodmanGvisorWarnOnce(t)

	var buf bytes.Buffer
	ui.SetWriter(&buf)
	t.Cleanup(func() { ui.SetWriter(os.Stderr) })

	warnPodmanGvisorUnverified()
	if buf.Len() == 0 {
		t.Fatal("warning should be observable after a reset")
	}

	resetPodmanGvisorWarnOnce(t)
	buf.Reset()
	warnPodmanGvisorUnverified()
	if buf.Len() == 0 {
		t.Error("reset should make the warning observable again")
	}
}
