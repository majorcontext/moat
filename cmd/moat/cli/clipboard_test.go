package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/run"
)

func TestSetupClipboardDisabled(t *testing.T) {
	stdin := strings.NewReader("input\x16")
	// A nil manager also verifies that disabling clipboard starts no watcher.
	got, cleanup := setupClipboard(context.Background(), nil, &run.Run{Clipboard: false}, stdin)
	defer cleanup()
	if got != stdin {
		t.Fatal("disabled clipboard changed the input reader")
	}
}

func TestSetupClipboardEnabledBeforeContainerStart(t *testing.T) {
	stdin := strings.NewReader("input\x16")
	// The watcher must wait for StateRunning. Canceling before startup must
	// leave the manager untouched and still restore the input wrapper cleanly.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got, cleanup := setupClipboard(ctx, nil, &run.Run{Clipboard: true, State: run.StateCreated}, stdin)
	defer cleanup()
	if got == stdin {
		t.Fatal("enabled clipboard did not wrap input")
	}
	cancel()
}
