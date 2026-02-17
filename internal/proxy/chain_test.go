package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
)

func TestChain_ExternalProxy(t *testing.T) {
	// Start a real upstream proxy that forwards requests
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Via-Upstream", "true")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("proxied"))
	}))
	defer upstream.Close()

	entries := []config.ProxyChainEntry{
		{
			Name: "test-proxy",
			URL:  upstream.URL,
		},
	}

	chain, err := NewChain(context.Background(), entries)
	if err != nil {
		t.Fatalf("NewChain failed: %v", err)
	}
	defer chain.Stop()

	if chain.UpstreamURL() == nil {
		t.Fatal("expected non-nil UpstreamURL")
	}
	if chain.UpstreamURL().String() != upstream.URL {
		t.Errorf("UpstreamURL = %q, want %q", chain.UpstreamURL().String(), upstream.URL)
	}

	names := chain.Names()
	if len(names) != 1 || names[0] != "test-proxy" {
		t.Errorf("Names = %v, want [test-proxy]", names)
	}
}

func TestChain_Nil(t *testing.T) {
	chain, err := NewChain(context.Background(), nil)
	if err != nil {
		t.Fatalf("NewChain failed: %v", err)
	}
	if chain != nil {
		t.Error("expected nil chain for empty entries")
	}

	// Nil chain methods should not panic
	var c *Chain
	if c.UpstreamURL() != nil {
		t.Error("expected nil UpstreamURL for nil chain")
	}
	if c.Names() != nil {
		t.Error("expected nil Names for nil chain")
	}
	if c.UpstreamTransport() != nil {
		t.Error("expected nil UpstreamTransport for nil chain")
	}
	c.Stop() // Should not panic
}

func TestChain_UpstreamTransport(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	entries := []config.ProxyChainEntry{
		{Name: "test", URL: upstream.URL},
	}

	chain, err := NewChain(context.Background(), entries)
	if err != nil {
		t.Fatalf("NewChain failed: %v", err)
	}
	defer chain.Stop()

	transport := chain.UpstreamTransport()
	if transport == nil {
		t.Fatal("expected non-nil transport")
	}
	if transport.Proxy == nil {
		t.Error("expected Proxy function to be set on transport")
	}
}

func TestChain_ValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		entries []config.ProxyChainEntry
		wantErr string
	}{
		{
			name: "invalid URL",
			entries: []config.ProxyChainEntry{
				{Name: "bad", URL: "://not-a-url"},
			},
			wantErr: "invalid URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewChain(context.Background(), tt.entries)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !containsStr(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestProxy_UpstreamTransport(t *testing.T) {
	// Start a mock upstream proxy that records whether requests go through it
	var upstreamHit bool
	upstreamProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
		// Forward to the actual target
		resp, err := http.DefaultTransport.RoundTrip(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, v := range resp.Header {
			for _, val := range v {
				w.Header().Add(k, val)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer upstreamProxy.Close()

	// Start a backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer backend.Close()

	// Create Moat proxy with upstream transport
	p := NewProxy()
	upstreamURL, _ := url.Parse(upstreamProxy.URL)
	p.SetUpstreamTransport(&http.Transport{
		Proxy: http.ProxyURL(upstreamURL),
	})

	// Make a request through Moat proxy
	req := httptest.NewRequest("GET", backend.URL, nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if !upstreamHit {
		t.Error("request did not go through upstream proxy")
	}
	if rec.Body.String() != "hello" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "hello")
	}
}

func TestFindFreePort(t *testing.T) {
	port, err := findFreePort()
	if err != nil {
		t.Fatalf("findFreePort failed: %v", err)
	}
	if port <= 0 {
		t.Errorf("expected positive port, got %d", port)
	}

	// Port should be usable
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("could not listen on free port %d: %v", port, err)
	}
	l.Close()
}

func TestWaitForPort(t *testing.T) {
	// Start a listener
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port
	if err := waitForPort(port, 2*time.Second); err != nil {
		t.Errorf("waitForPort failed for active port: %v", err)
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && searchStr(s, substr)
}

func searchStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
