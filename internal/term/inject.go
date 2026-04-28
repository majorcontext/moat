package term

import "io"

// InjectableReader wraps an io.Reader so another goroutine can splice bytes
// into the stream via Inject. Useful for sending synthetic keystrokes (e.g.
// Ctrl+L for redraw) to a child process without going through the user's
// stdin.
//
// Internally it runs an io.Copy goroutine that drains the underlying reader
// into an io.Pipe; Read returns whichever data — user input or injected
// bytes — arrives first. Injected bytes are interleaved at byte boundaries.
//
// Errors returned by the underlying reader propagate to Read via
// pw.CloseWithError, so wrapped readers can still signal sentinel errors
// (e.g. EscapeError) to consumers downstream.
//
// Inject blocks until the bytes have been consumed by Read or Close has been
// called.
type InjectableReader struct {
	pr *io.PipeReader
	pw *io.PipeWriter
}

// NewInjectableReader wraps r and starts a goroutine that copies from r into
// the pipe. The goroutine exits when r returns an error (including EOF) or
// when Close is called *and* r returns. Callers should call Close to release
// the pipe; the underlying reader is not closed.
func NewInjectableReader(r io.Reader) *InjectableReader {
	pr, pw := io.Pipe()
	ir := &InjectableReader{pr: pr, pw: pw}
	go func() {
		_, err := io.Copy(pw, r)
		_ = pw.CloseWithError(err)
	}()
	return ir
}

// Read implements io.Reader.
func (i *InjectableReader) Read(p []byte) (int, error) {
	return i.pr.Read(p)
}

// Inject splices b into the stream. The bytes appear in the next Read call
// (or are interleaved with concurrent user input at byte boundaries). Blocks
// until the bytes are consumed by Read or until Close, whichever comes first;
// returns io.ErrClosedPipe in the latter case.
func (i *InjectableReader) Inject(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	_, err := i.pw.Write(b)
	return err
}

// Close closes the pipe, causing pending and future Read calls to return EOF
// and pending Inject calls to return io.ErrClosedPipe. The underlying reader
// is not closed; the background copy goroutine exits when that reader returns.
func (i *InjectableReader) Close() error {
	return i.pw.Close()
}
