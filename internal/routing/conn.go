package routing

import (
	"bufio"
	"net"
)

// bufferedConn wraps a net.Conn with a bufio.Reader to allow peeking
// at the initial bytes without consuming them.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

// newBufferedConn wraps a connection with buffering for peeking.
func newBufferedConn(c net.Conn) *bufferedConn {
	return &bufferedConn{
		Conn: c,
		r:    bufio.NewReader(c),
	}
}

// Peek returns the next n bytes without advancing the reader.
func (bc *bufferedConn) Peek(n int) ([]byte, error) {
	return bc.r.Peek(n)
}

// Read reads data from the connection, including any buffered bytes.
func (bc *bufferedConn) Read(b []byte) (int, error) {
	return bc.r.Read(b)
}
