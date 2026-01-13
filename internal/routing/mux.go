package routing

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"github.com/andybons/agentops/internal/log"
)

const (
	protoTLS  = "tls"
	protoHTTP = "http"
)

// detectProtocol peeks at the first byte to determine TLS vs HTTP.
// TLS handshake records start with 0x16 (ContentType handshake).
func detectProtocol(bc *bufferedConn) string {
	// Set a deadline for the peek to avoid hanging on slow clients
	_ = bc.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer func() { _ = bc.SetReadDeadline(time.Time{}) }()

	b, err := bc.Peek(1)
	if err != nil || len(b) == 0 {
		// Default to HTTP on error (will fail gracefully)
		return protoHTTP
	}

	if b[0] == 0x16 {
		return protoTLS
	}
	return protoHTTP
}

// muxListener wraps a net.Listener to multiplex TLS and HTTP connections.
type muxListener struct {
	net.Listener
	tlsConfig *tls.Config
	handler   http.Handler
}

// newMuxListener creates a multiplexing listener.
func newMuxListener(ln net.Listener, tlsConfig *tls.Config, handler http.Handler) *muxListener {
	return &muxListener{
		Listener:  ln,
		tlsConfig: tlsConfig,
		handler:   handler,
	}
}

// serve accepts connections and handles them based on detected protocol.
func (ml *muxListener) serve() error {
	for {
		conn, err := ml.Accept()
		if err != nil {
			return err
		}
		go ml.handleConn(conn)
	}
}

func (ml *muxListener) handleConn(conn net.Conn) {
	bc := newBufferedConn(conn)
	proto := detectProtocol(bc)

	var netConn net.Conn = bc

	if proto == protoTLS {
		tlsConn := tls.Server(bc, ml.tlsConfig)
		if err := tlsConn.Handshake(); err != nil {
			log.Debug("TLS handshake failed", "remote", conn.RemoteAddr(), "error", err)
			conn.Close()
			return
		}
		netConn = tlsConn
	}

	// Create a single-connection listener to use with http.Serve
	scl := &singleConnListener{conn: netConn}
	server := &http.Server{
		Handler:           ml.handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	_ = server.Serve(scl)
}

// singleConnListener is a net.Listener that returns a single connection then closes.
type singleConnListener struct {
	conn net.Conn
	done bool
}

func (scl *singleConnListener) Accept() (net.Conn, error) {
	if scl.done {
		return nil, net.ErrClosed
	}
	scl.done = true
	return scl.conn, nil
}

func (scl *singleConnListener) Close() error   { return nil }
func (scl *singleConnListener) Addr() net.Addr { return scl.conn.LocalAddr() }
