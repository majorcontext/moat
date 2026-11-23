package storage

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func newTestLogWriter(t *testing.T) (*RunStore, *LogWriter) {
	t.Helper()
	s, err := NewRunStore(t.TempDir(), "run_logwriter1")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	w, err := s.LogWriter()
	if err != nil {
		t.Fatalf("LogWriter: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return s, w
}

func readLines(t *testing.T, s *RunStore) []string {
	t.Helper()
	entries, err := s.ReadLogs(0, 1<<20)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}
	lines := make([]string, len(entries))
	for i, e := range entries {
		if e.Timestamp.IsZero() {
			t.Errorf("entry %d has a zero timestamp", i)
		}
		lines[i] = e.Line
	}
	return lines
}

func assertLines(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLogWriterAssemblesLinesAcrossWrites(t *testing.T) {
	s, w := newTestLogWriter(t)

	for _, chunk := range []string{"hel", "lo wor", "ld\nsecond", "\n\nfourth\r\n"} {
		if n, err := w.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	// Completed lines are on disk before Close.
	assertLines(t, readLines(t, s), []string{"hello world", "second", "", "fourth"})
}

func TestLogWriterCloseFlushesFinalPartialLine(t *testing.T) {
	s, w := newTestLogWriter(t)

	w.Write([]byte("done\nprompt$ "))
	assertLines(t, readLines(t, s), []string{"done"})
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	assertLines(t, readLines(t, s), []string{"done", "prompt$ "})

	// A second Close neither fails nor repeats the final line.
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	assertLines(t, readLines(t, s), []string{"done", "prompt$ "})
	if _, err := w.Write([]byte("late\n")); !errors.Is(err, os.ErrClosed) {
		t.Errorf("Write after Close error = %v, want os.ErrClosed", err)
	}
}

func TestLogWriterCloseWithoutPartialLineAddsNothing(t *testing.T) {
	s, w := newTestLogWriter(t)

	w.Write([]byte("only\n"))
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	assertLines(t, readLines(t, s), []string{"only"})
}

func TestLogWriterSplitsLongLines(t *testing.T) {
	s, w := newTestLogWriter(t)

	// "é" is two bytes, so an odd prefix puts a character across the limit.
	long := "x" + strings.Repeat("é", maxLogLineBytes)
	w.Write([]byte(long + "\nafter\n"))

	lines := readLines(t, s)
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}
	for i, line := range lines[:3] {
		if len(line) > maxLogLineBytes {
			t.Errorf("line %d is %d bytes, over the %d limit", i, len(line), maxLogLineBytes)
		}
		if !utf8.ValidString(line) || strings.ContainsRune(line, utf8.RuneError) {
			t.Errorf("line %d split a character", i)
		}
	}
	if got := strings.Join(lines[:3], ""); got != long {
		t.Errorf("rejoined long line differs: %d bytes, want %d", len(got), len(long))
	}
	if lines[3] != "after" {
		t.Errorf("line after the long line = %q, want %q", lines[3], "after")
	}
}

func TestLogWriterKeepsLineAtLimitWhole(t *testing.T) {
	s, w := newTestLogWriter(t)

	exact := strings.Repeat("a", maxLogLineBytes)
	// The newline arrives in a later write, after the line reached the limit.
	w.Write([]byte(exact))
	w.Write([]byte("\nnext\n"))

	assertLines(t, readLines(t, s), []string{exact, "next"})
}

func TestLogWriterBoundsUnterminatedOutput(t *testing.T) {
	s, w := newTestLogWriter(t)

	chunk := []byte(strings.Repeat("\x1b[2J", 1024))
	for i := 0; i < 64; i++ {
		w.Write(chunk)
		if got := len(w.primary.pending); got > maxLogLineBytes {
			t.Fatalf("pending grew to %d bytes, over the %d limit", got, maxLogLineBytes)
		}
	}
	w.Close()

	// Escape bytes expand when encoded; ReadLogs must still read every entry.
	got := strings.Join(readLines(t, s), "")
	if want := strings.Repeat(string(chunk), 64); got != want {
		t.Errorf("read back %d bytes, want %d", len(got), len(want))
	}
}

func TestLogWriterStreamsDoNotJoinPartialLines(t *testing.T) {
	s, w := newTestLogWriter(t)
	stderr := w.Stream()

	w.Write([]byte("out "))
	stderr.Write([]byte("err "))
	w.Write([]byte("one\n"))
	stderr.Write([]byte("two\nerr tail"))
	w.Write([]byte("out tail"))
	w.Close()

	assertLines(t, readLines(t, s), []string{"out one", "err two", "out tail", "err tail"})
}

func TestLogWriterConcurrentStreams(t *testing.T) {
	s, w := newTestLogWriter(t)

	const perStream = 500
	var wg sync.WaitGroup
	for _, stream := range []struct {
		name string
		w    interface{ Write([]byte) (int, error) }
	}{{"out", w}, {"err", w.Stream()}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perStream; i++ {
				// Two writes per line, so interleaving would corrupt lines.
				fmt.Fprintf(stream.w, "%s-", stream.name)
				fmt.Fprintf(stream.w, "%d\n", i)
			}
		}()
	}
	wg.Wait()
	w.Close()

	next := map[string]int{}
	for _, line := range readLines(t, s) {
		name, num, ok := strings.Cut(line, "-")
		if !ok || num != fmt.Sprint(next[name]) {
			t.Fatalf("line %q out of order or corrupt; expected %s-%d", line, name, next[name])
		}
		next[name]++
	}
	if next["out"] != perStream || next["err"] != perStream {
		t.Errorf("got %d out and %d err lines, want %d each", next["out"], next["err"], perStream)
	}
}
