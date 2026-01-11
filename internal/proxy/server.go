package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
)

// Server wraps a Proxy in an HTTP server.
type Server struct {
	proxy    *Proxy
	server   *http.Server
	listener net.Listener
	addr     string
}

// NewServer creates a new proxy server.
func NewServer(proxy *Proxy) *Server {
	return &Server{
		proxy: proxy,
	}
}

// Start starts the proxy server on an available port.
func (s *Server) Start() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("creating listener: %w", err)
	}

	s.listener = listener
	s.addr = listener.Addr().String()

	s.server = &http.Server{
		Handler: s.proxy,
	}

	go s.server.Serve(listener)
	return nil
}

// Addr returns the proxy server address.
func (s *Server) Addr() string {
	return s.addr
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
