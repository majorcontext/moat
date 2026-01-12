//go:build integration

package routing

import (
	"context"
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

	// Start proxy lifecycle
	lc, err := NewLifecycle(dir, 0)
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}
	defer lc.Stop(context.Background())

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

	// Create HTTP client
	client := &http.Client{Timeout: 5 * time.Second}
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", lc.Port())

	// Test web service
	req, _ := http.NewRequest("GET", proxyURL+"/", nil)
	req.Host = "web.myapp.localhost"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Request to web service: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "web service" {
		t.Errorf("Web response = %q, want 'web service'", body)
	}

	// Test api service
	req, _ = http.NewRequest("GET", proxyURL+"/", nil)
	req.Host = "api.myapp.localhost"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Request to api service: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "api service" {
		t.Errorf("API response = %q, want 'api service'", body)
	}

	// Test default service (agent without service prefix)
	req, _ = http.NewRequest("GET", proxyURL+"/", nil)
	req.Host = "myapp.localhost"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Request to default service: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Default service status = %d, want 200", resp.StatusCode)
	}

	// Test unknown agent
	req, _ = http.NewRequest("GET", proxyURL+"/", nil)
	req.Host = "unknown.localhost"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Request to unknown: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Unknown agent status = %d, want 404", resp.StatusCode)
	}

	// Unregister and verify
	routes.Remove("myapp")
	req, _ = http.NewRequest("GET", proxyURL+"/", nil)
	req.Host = "web.myapp.localhost"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Request after remove: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("After remove status = %d, want 404", resp.StatusCode)
	}
}
