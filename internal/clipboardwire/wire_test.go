package clipboardwire

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	want := [][]byte{[]byte("line one\nline two\n\n"), []byte("日本語 🐱"), {}, bytes.Repeat([]byte("x"), MaxTextBytes)}
	var stream bytes.Buffer
	for _, data := range want {
		if err := Write(&stream, data); err != nil {
			t.Fatal(err)
		}
	}
	i := 0
	if err := Read(&stream, func(data []byte) {
		if i >= len(want) || !bytes.Equal(data, want[i]) {
			t.Fatalf("unexpected clipboard update %d", i)
		}
		i++
	}); err != nil {
		t.Fatal(err)
	}
	if i != len(want) {
		t.Fatalf("got %d updates, want %d", i, len(want))
	}
}

func TestRejectMalformedUpdates(t *testing.T) {
	for _, frame := range []string{"not base64!\n", "/w==\n", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), MaxTextBytes+1)) + "\n"} {
		if err := Read(strings.NewReader(frame), func([]byte) { t.Fatal("accepted invalid update") }); err == nil {
			t.Fatal("expected invalid frame error")
		}
	}
}
