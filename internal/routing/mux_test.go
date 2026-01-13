package routing

import (
	"net"
	"sync"
	"testing"
)

func TestMuxDetectsTLS(t *testing.T) {
	// Create a listener
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	var detected string
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(1)

	// Accept one connection and detect protocol
	go func() {
		defer wg.Done()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		bc := newBufferedConn(conn)
		proto := detectProtocol(bc)

		mu.Lock()
		detected = proto
		mu.Unlock()
	}()

	// Connect and send TLS handshake byte
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	conn.Write([]byte{0x16, 0x03, 0x01}) // TLS handshake
	conn.Close()

	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if detected != "tls" {
		t.Errorf("Protocol = %q, want 'tls'", detected)
	}
}

func TestMuxDetectsHTTP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	var detected string
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		bc := newBufferedConn(conn)
		proto := detectProtocol(bc)

		mu.Lock()
		detected = proto
		mu.Unlock()
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	conn.Write([]byte("GET / HTTP/1.1\r\n"))
	conn.Close()

	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if detected != "http" {
		t.Errorf("Protocol = %q, want 'http'", detected)
	}
}
