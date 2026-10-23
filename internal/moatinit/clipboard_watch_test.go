package moatinit

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/majorcontext/moat/internal/clipboardwire"
)

func TestClipboardBufferLimit(t *testing.T) {
	var b clipboardBuffer
	if _, err := io.Copy(&b, bytes.NewReader(bytes.Repeat([]byte("x"), clipboardwire.MaxTextBytes))); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("x")); err == nil {
		t.Fatal("accepted oversized clipboard data")
	}
	if b.buf.Len() != clipboardwire.MaxTextBytes {
		t.Fatalf("buffer grew beyond limit: %d", b.buf.Len())
	}
}

func TestReadClipboardTextChecksAdvertisedTargets(t *testing.T) {
	dir := t.TempDir()
	// xclip can return image bytes even when the caller requests UTF8_STRING.
	script := "#!/bin/sh\nfor arg do target=$arg; done\nif [ \"$target\" = TARGETS ]; then printf '%s' \"$TEST_CLIP_TARGETS\"; else printf '%s' \"$TEST_CLIP_DATA\"; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "xclip"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("TEST_CLIP_TARGETS", "TARGETS\nimage/png")
	t.Setenv("TEST_CLIP_DATA", "image bytes that happen to be valid text")
	if _, err := readClipboardText(context.Background()); err == nil {
		t.Fatal("image-only clipboard interpreted as text")
	}
	t.Setenv("TEST_CLIP_TARGETS", "TARGETS\nUTF8_STRING\nSTRING")
	want := "日本語\n\n"
	t.Setenv("TEST_CLIP_DATA", want)
	got, err := readClipboardText(context.Background())
	if err != nil || string(got) != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
}
