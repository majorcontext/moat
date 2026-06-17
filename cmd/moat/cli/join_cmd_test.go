package cli

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/provider"
)

// fakeJoinable implements provider.JoinableAgent for validation tests.
type fakeJoinable struct{ identifies bool }

func (f fakeJoinable) JoinCommand(provider.JoinOpts) ([]string, error) { return []string{"x"}, nil }
func (f fakeJoinable) IdentifiesAs(string) bool                        { return f.identifies }

func TestValidateJoinAgent_OK(t *testing.T) {
	if err := validateJoinAgent(fakeJoinable{identifies: true}, "claude", "claude-code"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateJoinAgent_WrongProvider(t *testing.T) {
	err := validateJoinAgent(fakeJoinable{identifies: false}, "codex", "claude-code")
	if err == nil || !strings.Contains(err.Error(), "no codex configuration") {
		t.Fatalf("got %v, want a clear 'no codex configuration' error", err)
	}
}

// TestResizePump_NoSendOnClosed verifies that resizePump never sends on a
// closed channel. It is intentionally run under -race to catch any concurrent
// close vs send on the out channel.
func TestResizePump_NoSendOnClosed(t *testing.T) {
	nopWinch := func() (container.TTYSize, bool) {
		return container.TTYSize{Width: 80, Height: 24}, true
	}

	t.Run("close_done_stops_pump_and_closes_out", func(t *testing.T) {
		done := make(chan struct{})
		sigCh := make(chan os.Signal, 1)
		out := make(chan container.TTYSize, 1)

		go resizePump(done, sigCh, nopWinch, out)

		// Closing done must cause resizePump to close out.
		close(done)

		// Drain out until it is closed; panic on send-on-closed would surface here.
		deadline := time.After(2 * time.Second)
		for {
			select {
			case _, ok := <-out:
				if !ok {
					return // out was closed cleanly — pass
				}
			case <-deadline:
				t.Fatal("timed out waiting for out to be closed after done was closed")
			}
		}
	})

	t.Run("sigwinch_then_close_done_no_panic", func(t *testing.T) {
		done := make(chan struct{})
		sigCh := make(chan os.Signal, 1)
		out := make(chan container.TTYSize, 1)

		go resizePump(done, sigCh, nopWinch, out)

		// Pre-load a SIGWINCH so resizePump has something to process.
		sigCh <- syscall.SIGWINCH

		// Close done concurrently with the buffered signal — exercises the race.
		close(done)

		deadline := time.After(2 * time.Second)
		for {
			select {
			case _, ok := <-out:
				if !ok {
					return // out was closed cleanly — pass
				}
			case <-deadline:
				t.Fatal("timed out waiting for out to be closed")
			}
		}
	})

	t.Run("concurrent_sigwinch_and_done", func(t *testing.T) {
		// Hammer the race detector: many goroutines send SIGWINCH while done
		// is closed, ensuring no send-on-closed slip through.
		for i := 0; i < 20; i++ {
			done := make(chan struct{})
			sigCh := make(chan os.Signal, 1)
			out := make(chan container.TTYSize, 2)

			go resizePump(done, sigCh, nopWinch, out)

			// Sender goroutine races with close(done).
			go func() {
				sigCh <- syscall.SIGWINCH
			}()

			close(done)

			deadline := time.After(2 * time.Second)
			drained := false
			for !drained {
				select {
				case _, ok := <-out:
					if !ok {
						drained = true
					}
				case <-deadline:
					t.Fatal("timed out waiting for out to be closed in concurrent test")
				}
			}
		}
	})
}
