//go:build integration

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
	"time"
)

func TestFullRoutingFlow(t *testing.T) {
	dir := t.TempDir()

	// Create two backend servers simulating container services
	webBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("web service"))
	}))
	defer webBackend.Close()

	apiBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("api service"))
	}))
	defer apiBackend.Close()

	// Start proxy lifecycle with TLS
	lc, err := NewLifecycle(dir, 0)
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}
	defer lc.Stop(context.Background())

	if _, err := lc.EnableTLS(); err != nil {
		t.Fatalf("EnableTLS: %v", err)
	}

	if err := lc.EnsureRunning(); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}

	// Register routes for "myapp"
	routes := lc.Routes()
	err = routes.Add("myapp", map[string]string{
		"web": webBackend.Listener.Addr().String(),
		"api": apiBackend.Listener.Addr().String(),
	})
	if err != nil {
		t.Fatalf("Add routes: %v", err)
	}

	// Create HTTP client (plain)
	httpClient := &http.Client{Timeout: 5 * time.Second}

	// Create HTTPS client
	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(lc.CA().CertPEM())
	httpsClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: certPool,
			},
		},
	}

	proxyAddr := fmt.Sprintf("127.0.0.1:%d", lc.Port())

	// Test HTTP web service
	req, _ := http.NewRequest("GET", "http://"+proxyAddr+"/", nil)
	req.Host = "web.myapp.localhost"
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP request to web service: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "web service" {
		t.Errorf("HTTP web response = %q, want 'web service'", body)
	}

	// Test HTTPS web service
	req, _ = http.NewRequest("GET", "https://"+proxyAddr+"/", nil)
	req.Host = "web.myapp.localhost"
	resp, err = httpsClient.Do(req)
	if err != nil {
		t.Fatalf("HTTPS request to web service: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "web service" {
		t.Errorf("HTTPS web response = %q, want 'web service'", body)
	}

	// Test HTTPS api service
	req, _ = http.NewRequest("GET", "https://"+proxyAddr+"/", nil)
	req.Host = "api.myapp.localhost"
	resp, err = httpsClient.Do(req)
	if err != nil {
		t.Fatalf("HTTPS request to api service: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "api service" {
		t.Errorf("HTTPS API response = %q, want 'api service'", body)
	}

	// Test default service (agent without service prefix)
	req, _ = http.NewRequest("GET", "https://"+proxyAddr+"/", nil)
	req.Host = "myapp.localhost"
	resp, err = httpsClient.Do(req)
	if err != nil {
		t.Fatalf("HTTPS request to default service: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Default service status = %d, want 200", resp.StatusCode)
	}

	// Test unknown agent
	req, _ = http.NewRequest("GET", "https://"+proxyAddr+"/", nil)
	req.Host = "unknown.localhost"
	resp, err = httpsClient.Do(req)
	if err != nil {
		t.Fatalf("HTTPS request to unknown: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Unknown agent status = %d, want 404", resp.StatusCode)
	}

	// Unregister and verify
	routes.Remove("myapp")
	req, _ = http.NewRequest("GET", "https://"+proxyAddr+"/", nil)
	req.Host = "web.myapp.localhost"
	resp, err = httpsClient.Do(req)
	if err != nil {
		t.Fatalf("HTTPS request after remove: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("After remove status = %d, want 404", resp.StatusCode)
	}
}
