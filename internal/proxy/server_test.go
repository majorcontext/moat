package proxy

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestServer_BindsToLocalhostOnly(t *testing.T) {
	// Security requirement: The proxy server must bind to localhost (127.0.0.1)
	// only, not to all interfaces (0.0.0.0), to prevent credential theft from
	// other hosts on the local network.

	p := NewProxy()
	s := NewServer(p)

	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer s.Stop(context.Background())

	addr := s.Addr()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q) error = %v", addr, err)
	}

	// The server must bind to 127.0.0.1, not 0.0.0.0 or [::]
	if host != "127.0.0.1" {
		t.Errorf("Server bound to %q, want %q (security: must not expose credentials to network)", host, "127.0.0.1")
	}
}

func TestServer_AcceptsLocalConnections(t *testing.T) {
	// Verify the server still accepts connections from localhost after the fix

	p := NewProxy()
	s := NewServer(p)

	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer s.Stop(context.Background())

	// Attempt to connect to the proxy server
	conn, err := net.DialTimeout("tcp", s.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("Failed to connect to proxy at %s: %v", s.Addr(), err)
	}
	conn.Close()
}

func TestServer_Port(t *testing.T) {
	p := NewProxy()
	s := NewServer(p)

	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer s.Stop(context.Background())

	port := s.Port()
	if port == "" || port == "0" {
		t.Errorf("Port() = %q, want non-zero port", port)
	}

	// Verify Port() matches the port from Addr()
	addr := s.Addr()
	if !strings.HasSuffix(addr, ":"+port) {
		t.Errorf("Addr() = %q doesn't end with Port() = %q", addr, port)
	}
}
