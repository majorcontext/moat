package moatinit

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xfixes"
	"github.com/jezek/xgb/xproto"
	"github.com/majorcontext/moat/internal/clipboardwire"
)

// WatchClipboard streams future X11 CLIPBOARD changes to the attached host.
// It never replays the existing clipboard on attach. Closing stdin ends the
// watcher, including when a host detaches from an otherwise running container.
// Stdout is reserved for the wire protocol; diagnostics must not be written there.
func WatchClipboard(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, stdin)
		cancel()
	}()

	// The exec may arrive while moat-init is still starting Xvfb.
	var conn *xgb.Conn
	var err error
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		conn, err = xgb.NewConnDisplay(":99")
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("connecting to clipboard display: %w", err)
		case <-tick.C:
		}
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, conn.Close)
	defer stop()
	if initErr := xfixes.Init(conn); initErr != nil {
		return initErr
	}
	if _, versionErr := xfixes.QueryVersion(conn, 5, 0).Reply(); versionErr != nil {
		return versionErr
	}
	selection, err := xproto.InternAtom(conn, false, uint16(len("CLIPBOARD")), "CLIPBOARD").Reply()
	if err != nil {
		return err
	}
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	if err := xfixes.SelectSelectionInputChecked(conn, root, selection.Atom,
		xfixes.SelectionEventMaskSetSelectionOwner).Check(); err != nil {
		return err
	}
	for {
		event, xerr := conn.WaitForEvent()
		if xerr != nil {
			return xerr
		}
		if event == nil {
			return ctx.Err()
		}
		notify, ok := event.(xfixes.SelectionNotifyEvent)
		if !ok || notify.Owner == 0 {
			continue
		}
		owner, ownerErr := xproto.GetSelectionOwner(conn, selection.Atom).Reply()
		if ownerErr != nil || owner.Owner != notify.Owner {
			continue // A newer owner has its own pending notification.
		}
		data, err := readClipboardText(ctx)
		if err != nil {
			continue // Image-only selections, exited owners, or unresponsive clients.
		}
		owner, ownerErr = xproto.GetSelectionOwner(conn, selection.Atom).Reply()
		if ownerErr != nil || owner.Owner != notify.Owner {
			continue // Selection changed while reading TARGETS or text.
		}
		if err := clipboardwire.Write(stdout, data); err != nil {
			return err
		}
	}
}

// xclip handles X11's INCR protocol for large selections. The command and its
// output are bounded so a clipboard owner cannot hang or exhaust the watcher.
func readClipboardText(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// xclip may return raw image bytes for UTF8_STRING even when it only
	// advertises image/png. Check TARGETS first to preserve host image pastes.
	targets, err := readSelection(ctx, "TARGETS")
	if err != nil {
		return nil, err
	}
	if !slices.Contains(strings.Fields(string(targets)), "UTF8_STRING") {
		return nil, fmt.Errorf("clipboard has no UTF-8 text target")
	}
	data, err := readSelection(ctx, "UTF8_STRING")
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("clipboard text is not UTF-8")
	}
	return data, nil
}

func readSelection(ctx context.Context, target string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "xclip", "-display", ":99", "-selection", "clipboard", "-out", "-target", target)
	var out clipboardBuffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.buf.Bytes(), nil
}

type clipboardBuffer struct{ buf bytes.Buffer }

func (b *clipboardBuffer) Write(p []byte) (int, error) {
	if len(p) > clipboardwire.MaxTextBytes-b.buf.Len() {
		return 0, fmt.Errorf("clipboard text exceeds limit")
	}
	return b.buf.Write(p)
}
