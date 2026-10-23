package clipboard

import (
	"context"
	"testing"
)

func TestBridgePasteDoesNotOverwriteNewHostClipboard(t *testing.T) {
	host := "pasted text"
	var sent, target string
	b := &Bridge{
		readHost:  func() (*Content, error) { return &Content{Data: []byte(host), MIMEType: "text/plain"}, nil },
		writeHost: func(data []byte) error { host = string(data); return nil },
		writeContainer: func(_ context.Context, data []byte, typ string) error {
			sent, target = string(data), typ
			return nil
		},
	}
	if err := b.Paste(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sent != host || target != "UTF8_STRING" {
		t.Fatalf("unexpected paste: %q (%s)", sent, target)
	}
	host = "new local copy"
	if err := b.Copy([]byte(sent)); err != nil {
		t.Fatal(err)
	}
	if host != "new local copy" {
		t.Fatal("paste echo replaced newer host clipboard")
	}
	// A later explicit copy of the same text is a new action, not an echo.
	_ = b.Copy([]byte(sent))
	if host != sent {
		t.Fatal("agent copy of previously pasted text was suppressed")
	}
	if err := b.Copy([]byte("agent copy\n")); err != nil {
		t.Fatal(err)
	}
	if host != "agent copy\n" {
		t.Fatalf("container copy not forwarded: %q", host)
	}
	// Once a different selection has arrived, the original text is a new copy.
	_ = b.Copy([]byte(sent))
	if host != sent {
		t.Fatal("new copy of previously pasted text was suppressed")
	}
	b.Close()
	_ = b.Copy([]byte("after detach"))
	_ = b.Paste(context.Background())
	if host != sent {
		t.Fatal("clipboard modified after detach")
	}
}

func TestBridgeImagePasteAndCanceledPaste(t *testing.T) {
	calls := 0
	b := &Bridge{
		readHost: func() (*Content, error) { return &Content{Data: []byte("png"), MIMEType: "image/png"}, nil },
		writeContainer: func(_ context.Context, data []byte, target string) error {
			calls++
			if string(data) != "png" || target != "image/png" {
				t.Fatalf("image paste changed: %q %q", data, target)
			}
			return nil
		},
	}
	if err := b.Paste(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.Paste(ctx); err != context.Canceled {
		t.Fatalf("canceled paste returned %v", err)
	}
	if calls != 1 {
		t.Fatalf("sent %d pastes", calls)
	}
}

func TestImagePasteClearsTextEchoSuppression(t *testing.T) {
	content := &Content{Data: []byte("text"), MIMEType: "text/plain"}
	copies := 0
	b := &Bridge{
		readHost:       func() (*Content, error) { return content, nil },
		writeContainer: func(context.Context, []byte, string) error { return nil },
		writeHost:      func([]byte) error { copies++; return nil },
	}
	if err := b.Paste(context.Background()); err != nil {
		t.Fatal(err)
	}
	content = &Content{Data: []byte("png"), MIMEType: "image/png"}
	if err := b.Paste(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := b.Copy([]byte("text")); err != nil {
		t.Fatal(err)
	}
	if copies != 1 {
		t.Fatal("image paste left old text copy suppressed")
	}
}
