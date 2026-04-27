package term

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

func TestInjectableReader_PassThrough(t *testing.T) {
	input := []byte("hello world")
	r := NewInjectableReader(bytes.NewReader(input))
	defer r.Close()

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Errorf("got %q, want %q", out, input)
	}
}

func TestInjectableReader_Inject(t *testing.T) {
	// Underlying reader blocks forever; we only ever see injected bytes.
	pr, pw := io.Pipe()
	defer pw.Close()
	r := NewInjectableReader(pr)
	defer r.Close()

	done := make(chan error, 1)
	go func() {
		done <- r.Inject([]byte{0x0C})
	}()

	buf := make([]byte, 4)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 || buf[0] != 0x0C {
		t.Errorf("got %v (n=%d), want [0x0C]", buf[:n], n)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Inject returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Inject did not return after Read consumed bytes")
	}
}

func TestInjectableReader_InjectInterleaved(t *testing.T) {
	// Drain the first chunk, then inject 0x0C and push more user input,
	// verifying both arrive in order. Inject runs in a goroutine because
	// it blocks until the bytes are consumed by Read.
	pr, pw := io.Pipe()
	defer pw.Close()
	r := NewInjectableReader(pr)
	defer r.Close()

	go func() {
		_, _ = pw.Write([]byte("ab"))
	}()

	buf := make([]byte, 8)
	got := make([]byte, 0, 8)

	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("first read error: %v", err)
	}
	got = append(got, buf[:n]...)

	injectErr := make(chan error, 1)
	go func() {
		injectErr <- r.Inject([]byte{0x0C})
	}()

	go func() {
		_, _ = pw.Write([]byte("c"))
	}()

	deadline := time.After(2 * time.Second)
	for len(got) < 4 {
		select {
		case <-deadline:
			t.Fatalf("timed out, got %v", got)
		default:
		}
		n, err = r.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil && err != io.EOF {
			t.Fatalf("drain read error: %v", err)
		}
	}

	if err := <-injectErr; err != nil {
		t.Errorf("inject error: %v", err)
	}

	// Inject and "c" race; both must appear after "ab" but their relative
	// order is non-deterministic.
	if string(got[:2]) != "ab" {
		t.Errorf("prefix: got %q, want \"ab\"", string(got[:2]))
	}
	if !bytes.Contains(got, []byte{0x0C}) {
		t.Errorf("missing injected byte; got %v", got)
	}
	if !bytes.Contains(got, []byte{'c'}) {
		t.Errorf("missing 'c'; got %v", got)
	}
}

func TestInjectableReader_InjectEmpty(t *testing.T) {
	r := NewInjectableReader(bytes.NewReader(nil))
	defer r.Close()
	if err := r.Inject(nil); err != nil {
		t.Errorf("Inject(nil) error: %v", err)
	}
	if err := r.Inject([]byte{}); err != nil {
		t.Errorf("Inject(empty) error: %v", err)
	}
}

func TestInjectableReader_CloseAfterEOF(t *testing.T) {
	r := NewInjectableReader(bytes.NewReader([]byte("x")))
	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("Close error: %v", err)
	}
}

// errReader returns the given bytes once, then a sentinel error. Used to
// verify that errors from the underlying reader propagate through the pipe
// rather than being silently converted to EOF.
type errReader struct {
	data []byte
	err  error
	done bool
}

func (r *errReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, r.err
	}
	r.done = true
	n := copy(p, r.data)
	return n, nil
}

func TestInjectableReader_PropagatesError(t *testing.T) {
	// Regression: io.Copy must propagate the underlying reader's error via
	// pw.CloseWithError, not swallow it. The escape proxy uses this path to
	// signal Ctrl+/ k via EscapeError; a previous version of this code
	// dropped the error and made stop a silent no-op.
	sentinel := errors.New("sentinel")
	r := NewInjectableReader(&errReader{data: []byte("hi"), err: sentinel})
	defer r.Close()

	got, err := io.ReadAll(r)
	if !errors.Is(err, sentinel) {
		t.Errorf("got err %v, want %v", err, sentinel)
	}
	if !bytes.Equal(got, []byte("hi")) {
		t.Errorf("got bytes %q, want \"hi\"", got)
	}
}

func TestInjectableReader_CloseUnblocksInject(t *testing.T) {
	// Inject blocks until Read consumes the bytes — or until Close. Verify
	// that Close releases a parked Inject with io.ErrClosedPipe rather than
	// hanging.
	pr, pw := io.Pipe()
	defer pw.Close()
	r := NewInjectableReader(pr)

	injectErr := make(chan error, 1)
	go func() {
		injectErr <- r.Inject([]byte{0x0C})
	}()

	// Give Inject a moment to park in pw.Write.
	time.Sleep(10 * time.Millisecond)

	if err := r.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	select {
	case err := <-injectErr:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Errorf("Inject returned %v, want io.ErrClosedPipe", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Inject did not return after Close")
	}
}
