package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Server wraps a Proxy in an HTTP server.
type Server struct {
	proxy    *Proxy
	server   *http.Server
	listener net.Listener
	addr     string
	bindAddr string // Address to bind to (default: 127.0.0.1)
	port     int    // Port to bind to (0 = OS-assigned)
}

// NewServer creates a new proxy server.
func NewServer(proxy *Proxy) *Server {
	return &Server{
		proxy:    proxy,
		bindAddr: "127.0.0.1", // Default: localhost only for security
	}
}

// SetBindAddr sets the address to bind to. Use "0.0.0.0" to bind to all
// interfaces (needed for Apple containers which access host via gateway IP).
// Must be called before Start().
func (s *Server) SetBindAddr(addr string) {
	s.bindAddr = addr
}

// SetPort sets the port to bind to. Use 0 (default) for an OS-assigned port.
// Must be called before Start().
func (s *Server) SetPort(port int) {
	s.port = port
}

// Start starts the proxy server on an available port.
// By default binds to localhost only to prevent credential exposure to other
// hosts on the network. Use SetBindAddr("0.0.0.0") before Start() to bind to
// all interfaces (needed for Apple containers).
func (s *Server) Start() error {
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.bindAddr, s.port))
	if err != nil {
		return fmt.Errorf("creating listener: %w", err)
	}

	s.listener = listener
	s.addr = listener.Addr().String()

	s.server = &http.Server{
		Handler:           s.proxy,
		ReadHeaderTimeout: 60 * time.Second, // Prevent Slowloris attacks
	}

	go func() {
		_ = s.server.Serve(listener) // Serve blocks until Shutdown is called
	}()
	return nil
}

// Addr returns the proxy server address (host:port).
func (s *Server) Addr() string {
	return s.addr
}

// Port returns just the port number the proxy is listening on.
func (s *Server) Port() string {
	_, port, _ := net.SplitHostPort(s.addr)
	return port
}

// Stop stops the proxy server.
func (s *Server) Stop(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// Proxy returns the underlying proxy.
func (s *Server) Proxy() *Proxy {
	return s.proxy
}
