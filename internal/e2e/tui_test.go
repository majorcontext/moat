//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/tui"
)

// =============================================================================
// TUI Writer Tests (Apple-only)
//
// These tests verify that the tui.Writer correctly processes Apple container
// output during the init phase. The key fix: processDataLocked and
// scheduleFooterRedrawLocked now run on every Write, even before the
// ready marker is detected. This ensures the footer is maintained and
// alt screen transitions are detected from the start.
//
// Note: In test mode, stdin is not a real TTY, so containers run without
// TTY allocation. The Apple CLI uses pipes instead of PTY, so ResizeTTY
// and PTY-specific behavior can't be tested here (see unit tests instead).
// =============================================================================

// TestAppleTUIWriterPassthrough verifies that container output flows through
// a tui.Writer correctly during the Apple init phase. Before the fix, the
// init phase called w.out.Write(p) directly (bypassing processDataLocked),
// which worked for passthrough but skipped alt screen detection and footer
// redraws. This test ensures the output still arrives correctly after the fix.
func TestAppleTUIWriterPassthrough(t *testing.T) {
	requireApple(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspace(t)
	testMarker := "tui-passthrough-test-12345"

	r, err := mgr.Create(ctx, run.Options{
		Name:        "e2e-tui-passthrough",
		Workspace:   workspace,
		Interactive: true,
		Cmd:         []string{"sh", "-c", "echo " + testMarker + "; echo line2; echo line3"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	// Create a tui.Writer with runtime="apple" to exercise the init phase.
	// This simulates what setupStatusBar does, but without requiring a real terminal.
	var outputBuf bytes.Buffer
	bar := tui.NewStatusBar(r.ID, r.Name, "apple")
	bar.SetDimensions(80, 24)
	writer := tui.NewWriter(&outputBuf, bar, "apple")
	_ = writer.Setup()
	defer writer.Cleanup()

	// Route container output through the tui.Writer
	err = mgr.StartAttached(ctx, r.ID, strings.NewReader(""), writer, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("StartAttached: %v", err)
	}

	// All output lines should pass through the tui.Writer, even during
	// the init phase when the ready marker hasn't been seen yet.
	output := outputBuf.String()
	for _, expected := range []string{testMarker, "line2", "line3"} {
		if !strings.Contains(output, expected) {
			t.Errorf("Missing %q in tui.Writer output (len=%d)", expected, len(output))
		}
	}
}

// TestAppleTUIWriterAltScreenDuringInit verifies that alt screen transitions
// sent by a container are detected by the tui.Writer even during the Apple
// init phase. Before the fix, processDataLocked was skipped during init,
// so alt screen enter/exit sequences passed through undetected. This meant
// the compositor was never activated and the footer was never drawn on the
// alt screen.
func TestAppleTUIWriterAltScreenDuringInit(t *testing.T) {
	requireApple(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspace(t)

	// Container enters alt screen, writes content, exits alt screen, writes more.
	// The tui.Writer should detect both transitions.
	r, err := mgr.Create(ctx, run.Options{
		Name:        "e2e-tui-altscreen",
		Workspace:   workspace,
		Interactive: true,
		Cmd: []string{"sh", "-c",
			// Enter alt screen, write content, exit alt screen, write normal content
			`printf '\033[?1049h'; echo 'in-alt-screen'; printf '\033[?1049l'; echo 'back-to-normal'`,
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	var outputBuf bytes.Buffer
	bar := tui.NewStatusBar(r.ID, r.Name, "apple")
	bar.SetDimensions(80, 24)
	writer := tui.NewWriter(&outputBuf, bar, "apple")
	_ = writer.Setup()
	defer writer.Cleanup()

	err = mgr.StartAttached(ctx, r.ID, strings.NewReader(""), writer, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("StartAttached: %v", err)
	}

	output := outputBuf.String()

	// The alt screen enter sequence should be present in the output —
	// enterCompositorLocked writes it to the real terminal output.
	if !strings.Contains(output, "\x1b[?1049h") {
		t.Error("Expected alt screen enter in output (compositor mode should have been activated)")
	}

	// After alt screen exit, normal output should appear.
	if !strings.Contains(output, "back-to-normal") {
		t.Errorf("Expected 'back-to-normal' after alt screen exit, got output len=%d", len(output))
	}

	// The alt screen exit should restore the scroll region (DECSTBM).
	// With height=24, the scroll region is [1;23].
	if !strings.Contains(output, "\x1b[1;23r") {
		t.Error("Expected DECSTBM restore after alt screen exit")
	}
}

// TestAppleTUIWriterMultipleWrites verifies that the tui.Writer handles
// many small writes during the Apple init phase without losing data or
// entering a bad state. This exercises the buffering and marker detection
// across multiple Write calls.
func TestAppleTUIWriterMultipleWrites(t *testing.T) {
	requireApple(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	workspace := createTestWorkspace(t)

	// Generate many output lines to exercise multiple Write calls
	r, err := mgr.Create(ctx, run.Options{
		Name:        "e2e-tui-multiwrite",
		Workspace:   workspace,
		Interactive: true,
		Cmd:         []string{"sh", "-c", "for i in $(seq 1 50); do echo \"output-line-$i\"; done"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	var outputBuf bytes.Buffer
	bar := tui.NewStatusBar(r.ID, r.Name, "apple")
	bar.SetDimensions(80, 24)
	writer := tui.NewWriter(&outputBuf, bar, "apple")
	_ = writer.Setup()
	defer writer.Cleanup()

	err = mgr.StartAttached(ctx, r.ID, strings.NewReader(""), writer, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("StartAttached: %v", err)
	}

	output := outputBuf.String()

	// Spot-check several lines from different points in the sequence
	for _, n := range []string{"1", "10", "25", "50"} {
		expected := "output-line-" + n
		if !strings.Contains(output, expected) {
			t.Errorf("Missing %q in output", expected)
		}
	}
}
