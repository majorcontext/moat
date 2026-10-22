package run

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/clipboardwire"
	"github.com/majorcontext/moat/internal/container"
)

type clipboardRuntime struct {
	stubRuntime
	exec func(container.ExecOptions) error
}

func (r *clipboardRuntime) ExecInteractive(_ context.Context, _ string, cmd []string, opts container.ExecOptions) error {
	if !slices.Equal(cmd, []string{"/usr/local/bin/moat-init", "--watch-clipboard"}) || opts.TTY {
		return errors.New("watcher must use a dedicated non-TTY stream")
	}
	return r.exec(opts)
}

func TestWatchClipboardCopiesAndClosesStdinOnDetach(t *testing.T) {
	stopped := make(chan struct{})
	rt := &clipboardRuntime{exec: func(opts container.ExecOptions) error {
		defer close(stopped)
		if err := clipboardwire.Write(opts.Stdout, []byte("copied\n")); err != nil {
			return err
		}
		_, err := io.Copy(io.Discard, opts.Stdin)
		return err
	}}
	m := mgrWithRuntime(rt)
	m.runs = map[string]*Run{"run_test": {ID: "run_test", State: StateRunning}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	updates := make(chan string, 1)
	go func() {
		done <- m.WatchClipboard(ctx, "run_test", func(data []byte) { updates <- string(data) })
	}()
	select {
	case data := <-updates:
		if data != "copied\n" {
			t.Fatalf("clipboard data changed: %q", data)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("missing copy update")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watch did not stop on cancellation")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("watcher stdin remained open after detach")
	}
}

func TestWatchClipboardPropagatesExecError(t *testing.T) {
	want := errors.New("helper unavailable")
	m := mgrWithRuntime(&clipboardRuntime{exec: func(container.ExecOptions) error { return want }})
	m.runs = map[string]*Run{"run_test": {ID: "run_test", State: StateRunning}}
	if err := m.WatchClipboard(context.Background(), "run_test", func([]byte) { t.Fatal("unexpected clipboard write") }); !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}
