package cli

import (
	"context"
	"errors"
	"io"
	"time"

	clipboardpkg "github.com/majorcontext/moat/internal/clipboard"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/term"
	"github.com/majorcontext/moat/internal/ui"
)

// setupClipboard owns both directions of clipboard bridging for an attachment.
func setupClipboard(ctx context.Context, manager *run.Manager, r *run.Run, stdin io.Reader) (io.Reader, func()) {
	if !r.Clipboard {
		return stdin, func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	bridge := clipboardpkg.NewBridge(func(ctx context.Context, data []byte, target string) error {
		return manager.WriteClipboard(ctx, r.ID, data, target)
	})
	go func() {
		// Primary attachment starts before the container. A join is already running.
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for r.GetState() != run.StateRunning {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
		err := manager.WatchClipboard(ctx, r.ID, func(data []byte) {
			if err := bridge.Copy(data); err != nil {
				log.Debug("failed to copy container text to host clipboard", "error", err)
			}
		})
		if err != nil && ctx.Err() == nil {
			log.Debug("container clipboard watcher stopped", "error", err)
			// A normal container shutdown can end the exec before this
			// attachment is canceled. Warn only about a missing helper.
			var execErr *container.ExecError
			if r.GetState() == run.StateRunning && errors.As(err, &execErr) && execErr.ExitCode == 127 {
				ui.Warn("Container-to-host clipboard sync is unavailable. Start a new run if this container predates your Moat upgrade.")
			}
		}
	}()
	reader := term.NewClipboardProxy(stdin, func() {
		clipCtx, clipCancel := context.WithTimeout(ctx, 3*time.Second)
		defer clipCancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := bridge.Paste(clipCtx); err != nil {
				log.Debug("failed to paste host clipboard into container", "error", err)
			}
		}()
		select {
		case <-done:
		case <-clipCtx.Done():
		}
	})
	return reader, func() {
		cancel()
		bridge.Close()
	}
}
