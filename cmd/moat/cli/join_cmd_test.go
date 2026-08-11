package cli

import (
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/run"
)

// fakeJoinable implements provider.JoinableAgent for validation tests. It is
// also reused by joinpick_test.go.
type fakeJoinable struct{ names []string }

func (f fakeJoinable) JoinCommand(provider.JoinOpts) ([]string, error) { return nil, nil }
func (f fakeJoinable) IdentifiesAs(agent string) bool {
	return slices.Contains(f.names, agent)
}

func TestValidateJoinAgent(t *testing.T) {
	claude := fakeJoinable{names: []string{"claude", "claude-code"}}

	tests := []struct {
		name    string
		run     *run.Run
		agent   string
		wantErr bool
		errHas  string
	}{
		{
			name:  "member of the capability set is accepted",
			run:   &run.Run{ID: "run_1", JoinableAgents: []string{"claude"}},
			agent: "claude",
		},
		{
			name:    "non-member is rejected even when the agent string matches",
			run:     &run.Run{ID: "run_1", Agent: "claude", JoinableAgents: []string{"codex"}},
			agent:   "claude",
			wantErr: true,
			errHas:  "codex",
		},
		{
			name:    "empty set refuses",
			run:     &run.Run{ID: "run_1", Agent: "claude", JoinableAgents: []string{}},
			agent:   "claude",
			wantErr: true,
		},
		{
			name:  "nil set falls back and accepts a matching agent string",
			run:   &run.Run{ID: "run_1", Agent: "claude", JoinableAgents: nil},
			agent: "claude",
		},
		{
			name:    "nil set falls back and refuses a stale agent string",
			run:     &run.Run{ID: "run_1", Agent: "vibrant-code", JoinableAgents: nil},
			agent:   "claude",
			wantErr: true,
			errHas:  "Recreate the run",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateJoinAgent(claude, tt.agent, tt.run)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateJoinAgent() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.errHas != "" && !strings.Contains(err.Error(), tt.errHas) {
				t.Errorf("error %q should mention %q", err, tt.errHas)
			}
		})
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
