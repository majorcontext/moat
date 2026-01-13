package routing

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

// mockConn implements net.Conn for testing
type mockConn struct {
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
}

func newMockConn(data []byte) *mockConn {
	return &mockConn{
		readBuf:  bytes.NewBuffer(data),
		writeBuf: &bytes.Buffer{},
	}
}

func (m *mockConn) Read(b []byte) (int, error)         { return m.readBuf.Read(b) }
func (m *mockConn) Write(b []byte) (int, error)        { return m.writeBuf.Write(b) }
func (m *mockConn) Close() error                       { return nil }
func (m *mockConn) LocalAddr() net.Addr                { return nil }
func (m *mockConn) RemoteAddr() net.Addr               { return nil }
func (m *mockConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error { return nil }

func TestBufferedConn(t *testing.T) {
	data := []byte("hello world")
	mock := newMockConn(data)

	bc := newBufferedConn(mock)

	// Peek first byte
	peeked, err := bc.Peek(1)
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if peeked[0] != 'h' {
		t.Errorf("Peeked = %q, want 'h'", peeked[0])
	}

	// Read all - should include the peeked byte
	all, err := io.ReadAll(bc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(all) != "hello world" {
		t.Errorf("ReadAll = %q, want 'hello world'", all)
	}
}

func TestBufferedConnDetectTLS(t *testing.T) {
	// TLS handshake starts with 0x16 (handshake record type)
	tlsData := []byte{0x16, 0x03, 0x01, 0x00, 0x05}
	mock := newMockConn(tlsData)
	bc := newBufferedConn(mock)

	peeked, _ := bc.Peek(1)
	if peeked[0] != 0x16 {
		t.Errorf("TLS detection failed: got %x, want 0x16", peeked[0])
	}
}

func TestBufferedConnDetectHTTP(t *testing.T) {
	// HTTP request starts with method (GET, POST, etc.)
	httpData := []byte("GET / HTTP/1.1\r\n")
	mock := newMockConn(httpData)
	bc := newBufferedConn(mock)

	peeked, _ := bc.Peek(1)
	if peeked[0] != 'G' {
		t.Errorf("HTTP detection failed: got %c, want 'G'", peeked[0])
	}
}
