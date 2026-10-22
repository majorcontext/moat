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
