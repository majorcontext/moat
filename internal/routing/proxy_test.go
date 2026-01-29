package routing

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/majorcontext/moat/internal/proxy"
)

func TestReverseProxy(t *testing.T) {
	// Create a backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello from backend"))
	}))
	defer backend.Close()

	// Create route table with backend
	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	routes.Add("myapp", map[string]string{
		"web": backend.Listener.Addr().String(),
	})

	// Create reverse proxy
	rp := NewReverseProxy(routes)

	// Test routing via Host header
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "web.myapp.localhost:8080"
	rec := httptest.NewRecorder()

	rp.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rec.Code)
	}

	body, _ := io.ReadAll(rec.Body)
	if string(body) != "hello from backend" {
		t.Errorf("Body = %q, want 'hello from backend'", body)
	}
}

func TestReverseProxyUnknownAgent(t *testing.T) {
	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	rp := NewReverseProxy(routes)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "web.unknown.localhost:8080"
	rec := httptest.NewRecorder()

	rp.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", rec.Code)
	}
}

func TestReverseProxyDefaultService(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("default"))
	}))
	defer backend.Close()

	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	routes.Add("myapp", map[string]string{
		"web": backend.Listener.Addr().String(),
	})

	rp := NewReverseProxy(routes)

	// Request without service prefix
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "myapp.localhost:8080"
	rec := httptest.NewRecorder()

	rp.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rec.Code)
	}
}

func TestProxyServerWithTLS(t *testing.T) {
	// Create a backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secure backend"))
	}))
	defer backend.Close()

	// Create route table with backend
	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	routes.Add("myapp", map[string]string{
		"web": backend.Listener.Addr().String(),
	})

	// Create CA for TLS
	caDir := t.TempDir()
	ca, err := proxy.NewCA(caDir)
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}

	// Create proxy server with TLS enabled
	ps := NewProxyServer(routes)
	ps.EnableTLS(ca)
	if err := ps.Start(0); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ps.Stop(context.Background())

	// Create HTTPS client that trusts our CA
	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(ca.CertPEM())
	tlsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    certPool,
				ServerName: "web.myapp.localhost", // SNI for cert generation
				MinVersion: tls.VersionTLS12,
			},
		},
	}

	// Test HTTPS request
	url := fmt.Sprintf("https://127.0.0.1:%d/", ps.Port())
	req, _ := http.NewRequest("GET", url, nil)
	req.Host = "web.myapp.localhost"
	resp, err := tlsClient.Do(req)
	if err != nil {
		t.Fatalf("HTTPS request: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "secure backend" {
		t.Errorf("Body = %q, want 'secure backend'", body)
	}
}

func TestProxyServerWithTLSAndHTTP(t *testing.T) {
	// Create a backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("backend response"))
	}))
	defer backend.Close()

	// Create route table with backend
	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	routes.Add("myapp", map[string]string{
		"web": backend.Listener.Addr().String(),
	})

	// Create CA for TLS
	caDir := t.TempDir()
	ca, err := proxy.NewCA(caDir)
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}

	// Create proxy server with TLS enabled
	ps := NewProxyServer(routes)
	ps.EnableTLS(ca)
	if err := ps.Start(0); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ps.Stop(context.Background())

	// Test HTTPS request
	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(ca.CertPEM())
	tlsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    certPool,
				ServerName: "web.myapp.localhost", // SNI for cert generation
				MinVersion: tls.VersionTLS12,
			},
		},
	}

	httpsURL := fmt.Sprintf("https://127.0.0.1:%d/", ps.Port())
	req, _ := http.NewRequest("GET", httpsURL, nil)
	req.Host = "web.myapp.localhost"
	resp, err := tlsClient.Do(req)
	if err != nil {
		t.Fatalf("HTTPS request: %v", err)
	}
	resp.Body.Close()

	// Test plain HTTP request on same port
	httpClient := &http.Client{}
	httpURL := fmt.Sprintf("http://127.0.0.1:%d/", ps.Port())
	req, _ = http.NewRequest("GET", httpURL, nil)
	req.Host = "web.myapp.localhost"
	resp, err = httpClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP request: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "backend response" {
		t.Errorf("Body = %q, want 'backend response'", body)
	}
}

func TestEnableTLSValidation(t *testing.T) {
	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	ps := NewProxyServer(routes)

	caDir := t.TempDir()
	ca, err := proxy.NewCA(caDir)
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}

	// First call should succeed
	if err := ps.EnableTLS(ca); err != nil {
		t.Errorf("First EnableTLS call failed: %v", err)
	}

	// Second call should fail
	if err := ps.EnableTLS(ca); err == nil {
		t.Error("Expected error when calling EnableTLS twice, got nil")
	} else if err.Error() != "TLS already enabled" {
		t.Errorf("Expected 'TLS already enabled' error, got: %v", err)
	}
}

func TestEnableTLSAfterStart(t *testing.T) {
	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	ps := NewProxyServer(routes)

	// Start the server first
	if err := ps.Start(0); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ps.Stop(context.Background())

	caDir := t.TempDir()
	ca, err := proxy.NewCA(caDir)
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}

	// EnableTLS after Start should fail
	if err := ps.EnableTLS(ca); err == nil {
		t.Error("Expected error when calling EnableTLS after Start, got nil")
	} else if err.Error() != "cannot enable TLS after Start()" {
		t.Errorf("Expected 'cannot enable TLS after Start()' error, got: %v", err)
	}
}
