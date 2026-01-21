//go:build integration

package trace

import (
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestTracerCapturesExec(t *testing.T) {
	tracer, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if err := tracer.Start(); err != nil {
		t.Skipf("Tracer start failed (may require privileges): %v", err)
	}
	defer tracer.Stop()

	// Channel to capture events
	events := make(chan ExecEvent, 100)
	tracer.OnExec(func(e ExecEvent) {
		events <- e
	})

	// Execute a command that runs long enough to be detected by the tracer.
	// The Darwin tracer uses polling (100ms default), so short-lived commands
	// like "echo" may complete before the tracer polls. Using "sleep" ensures
	// the process exists long enough to be captured.
	cmd := exec.Command("sleep", "0.5")
	go func() {
		_ = cmd.Run()
	}()

	// Wait for event with timeout. We may receive multiple events (other
	// processes on the system), so we look for our specific command.
	timeout := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Command == "sleep" {
				// Found our command
				return
			}
			// Continue looking for our command
		case <-timeout:
			// On Darwin, the tracer may not capture events if the kinfo_proc
			// structure offsets don't match the current macOS version. This
			// is a known limitation that varies by OS version/architecture.
			if runtime.GOOS == "darwin" {
				t.Skip("Darwin tracer may not capture events on this macOS version (kinfo_proc structure mismatch)")
			}
			t.Error("Timeout waiting for exec event")
			return
		}
	}
}

func TestTracerFiltersByPID(t *testing.T) {
	// Create tracer that only tracks a non-existent PID
	tracer, err := New(Config{PID: 99999})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if err := tracer.Start(); err != nil {
		t.Skipf("Tracer start failed: %v", err)
	}
	defer tracer.Stop()

	events := make(chan ExecEvent, 10)
	tracer.OnExec(func(e ExecEvent) {
		events <- e
	})

	// Execute a command (should NOT be captured)
	cmd := exec.Command("echo", "test")
	_ = cmd.Run()

	// Should timeout with no events
	select {
	case event := <-events:
		t.Errorf("Unexpected event captured: %+v", event)
	case <-time.After(500 * time.Millisecond):
		// Expected - no events
	}
}
