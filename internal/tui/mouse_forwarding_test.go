package tui

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// syncBuf is a goroutine-safe io.Writer. The compositor render loop writes to
// the host output from a background goroutine, so tests read through a mutex.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

const (
	altEnter = "\x1b[?1049h"
	altExit  = "\x1b[?1049l"
)

func newMouseTestWriter(t *testing.T, out *syncBuf) *Writer {
	t.Helper()
	bar := NewStatusBar("run_test", "session", "docker")
	bar.SetDimensions(80, 24)
	w := NewWriter(out, bar, "docker")
	if err := w.Setup(); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return w
}

func writeParts(t *testing.T, w *Writer, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if _, err := w.Write([]byte(p)); err != nil {
			t.Fatalf("Write(%q): %v", p, err)
		}
	}
}

// TestCompositorForwardsMouseDisableSoHostNotLeftReporting reproduces the
// captured production bug (run_c46269eeedc2): the child enables mouse in scroll
// mode (passes through to the host), opens a fullscreen view (alt screen), then
// disables mouse and closes the view. On main the disable is emitted while moat
// is in compositor mode, so it is swallowed by the VT emulator and never
// reaches the host — leaving the terminal stuck reporting mouse events after
// the child returns to its inline UI, so the wheel can no longer scroll.
func TestCompositorForwardsMouseDisableSoHostNotLeftReporting(t *testing.T) {
	out := &syncBuf{}
	w := newMouseTestWriter(t, out)
	defer func() { _ = w.Cleanup() }()

	// Open: enable mouse (scroll mode -> passthrough), then enter alt screen.
	writeParts(t, w, "\x1b[?1000h\x1b[?1006h", altEnter)
	// Close: disable mouse (compositor mode), then exit alt screen.
	writeParts(t, w, "\x1b[?1006l\x1b[?1000l", altExit)

	host := out.String()
	if !strings.Contains(host, "\x1b[?1000l") || !strings.Contains(host, "\x1b[?1006l") {
		t.Fatalf("host never received the mouse-disable; reporting left stuck on (the leak)")
	}
}

// TestCompositorForwardsMouseEnableToHost: a mouse-enable emitted while already
// in the alternate screen must reach the host so the fullscreen view actually
// receives mouse events (instead of the host falling back to alternate-scroll).
func TestCompositorForwardsMouseEnableToHost(t *testing.T) {
	out := &syncBuf{}
	w := newMouseTestWriter(t, out)
	defer func() { _ = w.Cleanup() }()

	writeParts(t, w, altEnter, "\x1b[?1006h")

	if !strings.Contains(out.String(), "\x1b[?1006h") {
		t.Fatalf("host never received the mouse-enable emitted inside the alt screen")
	}
}

// TestCompositorMouseModeSplitAcrossWrites: a mouse-mode sequence split across
// two Write calls must still be reassembled and forwarded.
func TestCompositorMouseModeSplitAcrossWrites(t *testing.T) {
	out := &syncBuf{}
	w := newMouseTestWriter(t, out)
	defer func() { _ = w.Cleanup() }()

	writeParts(t, w, altEnter)
	writeParts(t, w, "\x1b[?100", "2h") // "\x1b[?1002h" split mid-sequence

	if !strings.Contains(out.String(), "\x1b[?1002h") {
		t.Fatalf("split mouse-enable was not reassembled and forwarded to host")
	}
}

// TestCleanupDisablesForwardedMouseModes: if moat forwarded a mouse-enable on
// the child's behalf during compositor mode and the child exits/crashes without
// disabling it, teardown must disable it so the user's terminal is not left
// stuck reporting mouse events after moat exits.
func TestCleanupDisablesForwardedMouseModes(t *testing.T) {
	out := &syncBuf{}
	w := newMouseTestWriter(t, out)

	writeParts(t, w, altEnter, "\x1b[?1000h\x1b[?1006h") // enabled inside compositor
	if err := w.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	host := out.String()
	if !strings.Contains(host, "\x1b[?1000l") || !strings.Contains(host, "\x1b[?1006l") {
		t.Fatalf("Cleanup did not disable forwarded mouse modes; terminal left reporting")
	}
}

// TestCompositorDoesNotForwardNonMouseModes (companion/guard): only mouse modes
// are host-global and forwarded. A non-mouse private mode (here bracketed paste)
// must NOT be forwarded to the host — it belongs to the emulator.
func TestCompositorDoesNotForwardNonMouseModes(t *testing.T) {
	out := &syncBuf{}
	w := newMouseTestWriter(t, out)
	defer func() { _ = w.Cleanup() }()

	writeParts(t, w, altEnter, "\x1b[?2004h") // bracketed paste — not a mouse mode

	if strings.Contains(out.String(), "\x1b[?2004h") {
		t.Fatalf("non-mouse private mode ?2004h was forwarded to the host")
	}
}

// TestScrollModePassesMouseModesThrough (companion/guard): scroll mode is
// unchanged — the child's mouse sequences pass straight through to the host.
func TestScrollModePassesMouseModesThrough(t *testing.T) {
	out := &syncBuf{}
	w := newMouseTestWriter(t, out)
	defer func() { _ = w.Cleanup() }()

	writeParts(t, w, "\x1b[?1006h") // scroll mode (no alt screen entered)

	if !strings.Contains(out.String(), "\x1b[?1006h") {
		t.Fatalf("scroll-mode mouse mode was not passed through to host")
	}
}
