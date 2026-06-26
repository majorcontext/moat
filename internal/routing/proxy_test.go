package routing

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/majorcontext/gatekeeper/proxy"
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

func TestReverseProxyRejectsNonLocalhostHost(t *testing.T) {
	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	rp := NewReverseProxy(routes)

	// Bare localhost / 127.0.0.1 are intentionally excluded here: they serve
	// the discovery index (see TestGlobalIndex), not a 400.
	tests := []struct {
		name string
		host string
	}{
		{"external domain", "google.com"},
		{"external domain with port", "google.com:8080"},
		{"ip address", "192.168.1.1:8080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()

			rp.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("Host %q: status = %d, want 400", tt.host, rec.Code)
			}
		})
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

func TestGlobalIndex(t *testing.T) {
	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	routes.Add("demo", map[string]string{
		"web": "127.0.0.1:3000",
		"api": "127.0.0.1:8080",
	})
	rp := NewReverseProxy(routes)

	t.Run("html", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = "localhost:8080"
		rec := httptest.NewRecorder()

		rp.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Errorf("Content-Type = %q, want text/html", ct)
		}
		body := rec.Body.String()
		for _, want := range []string{"demo", "http://web.demo.localhost:8080", "http://api.demo.localhost:8080"} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q\n%s", want, body)
			}
		}
	})

	t.Run("json", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = "localhost:8080"
		req.Header.Set("Accept", "application/json")
		rec := httptest.NewRecorder()

		rp.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var payload struct {
			Agents []struct {
				Name      string `json:"name"`
				BaseURL   string `json:"base_url"`
				Endpoints []struct {
					Name string `json:"name"`
					URL  string `json:"url"`
				} `json:"endpoints"`
			} `json:"agents"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal: %v\n%s", err, rec.Body.String())
		}
		if len(payload.Agents) != 1 || payload.Agents[0].Name != "demo" {
			t.Fatalf("agents = %+v, want one agent 'demo'", payload.Agents)
		}
		if payload.Agents[0].BaseURL != "http://demo.localhost:8080" {
			t.Errorf("base_url = %q", payload.Agents[0].BaseURL)
		}
		// Endpoints are sorted by name: api before web.
		eps := payload.Agents[0].Endpoints
		if len(eps) != 2 || eps[0].Name != "api" || eps[1].Name != "web" {
			t.Fatalf("endpoints = %+v, want sorted [api web]", eps)
		}
		if eps[1].URL != "http://web.demo.localhost:8080" {
			t.Errorf("web url = %q", eps[1].URL)
		}
	})

	t.Run("empty", func(t *testing.T) {
		emptyRoutes, _ := NewRouteTable(t.TempDir())
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = "localhost:8080"
		rec := httptest.NewRecorder()

		NewReverseProxy(emptyRoutes).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "No agents") {
			t.Errorf("expected empty-state message, got:\n%s", rec.Body.String())
		}
	})
}

func TestAgentIndexUsesRequestScheme(t *testing.T) {
	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	routes.Add("demo", map[string]string{
		"web": "127.0.0.1:3000",
		"api": "127.0.0.1:8080",
	})
	rp := NewReverseProxy(routes)

	t.Run("html uses request scheme", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = "demo.localhost:8080"
		req.TLS = &tls.ConnectionState{} // simulate HTTPS
		rec := httptest.NewRecorder()

		rp.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "https://web.demo.localhost:8080") {
			t.Errorf("expected https links, got:\n%s", body)
		}
	})

	// The agent index JSON serializes an agentEntry at the top level (name,
	// base_url, endpoints) — distinct from the global index's {"agents":[…]}
	// wrapper. Lock that contract in so a shared-wrapper refactor can't change
	// it silently.
	t.Run("json shape is a bare agentEntry", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = "demo.localhost:8080"
		req.Header.Set("Accept", "application/json")
		rec := httptest.NewRecorder()

		rp.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var entry struct {
			Name      string `json:"name"`
			BaseURL   string `json:"base_url"`
			Endpoints []struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"endpoints"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &entry); err != nil {
			t.Fatalf("unmarshal: %v\n%s", err, rec.Body.String())
		}
		if entry.Name != "demo" {
			t.Errorf("name = %q, want demo", entry.Name)
		}
		if entry.BaseURL != "http://demo.localhost:8080" {
			t.Errorf("base_url = %q", entry.BaseURL)
		}
		urls := map[string]string{}
		for _, e := range entry.Endpoints {
			urls[e.Name] = e.URL
		}
		if urls["web"] != "http://web.demo.localhost:8080" || urls["api"] != "http://api.demo.localhost:8080" {
			t.Errorf("endpoints = %+v", entry.Endpoints)
		}
	})
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
	if err := ps.EnableTLS(ca); err != nil {
		t.Fatalf("EnableTLS: %v", err)
	}
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
	if err := ps.EnableTLS(ca); err != nil {
		t.Fatalf("EnableTLS: %v", err)
	}
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

// TestReverseProxyMultiEndpoint tests host-based routing with multiple endpoints
// on the same agent, matching the examples/multi-endpoint scenario.
func TestReverseProxyMultiEndpoint(t *testing.T) {
	// Create separate backend servers for each endpoint
	webBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("web endpoint"))
	}))
	defer webBackend.Close()

	apiBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("api endpoint"))
	}))
	defer apiBackend.Close()

	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	routes.Add("demo", map[string]string{
		"web": webBackend.Listener.Addr().String(),
		"api": apiBackend.Listener.Addr().String(),
	})

	rp := NewReverseProxy(routes)

	tests := []struct {
		name             string
		host             string
		wantCode         int
		wantBody         string
		wantBodyContains []string
	}{
		{
			name:     "web endpoint with port",
			host:     "web.demo.localhost:8080",
			wantCode: http.StatusOK,
			wantBody: "web endpoint",
		},
		{
			name:     "api endpoint with port",
			host:     "api.demo.localhost:8080",
			wantCode: http.StatusOK,
			wantBody: "api endpoint",
		},
		{
			name:     "web endpoint without port",
			host:     "web.demo.localhost",
			wantCode: http.StatusOK,
			wantBody: "web endpoint",
		},
		{
			name:     "api endpoint without port",
			host:     "api.demo.localhost",
			wantCode: http.StatusOK,
			wantBody: "api endpoint",
		},
		{
			name:     "agent only serves discovery index",
			host:     "demo.localhost:8080",
			wantCode: http.StatusOK,
			// Multiple endpoints -> bare agent host serves the index instead
			// of guessing which endpoint to proxy to.
			wantBodyContains: []string{"web.demo.localhost:8080", "api.demo.localhost:8080"},
		},
		{
			name:     "unknown endpoint serves discovery index",
			host:     "unknown.demo.localhost:8080",
			wantCode: http.StatusOK,
			// Unknown endpoint name falls through to the agent index so the
			// caller can discover the valid endpoint names.
			wantBodyContains: []string{"web.demo.localhost:8080", "api.demo.localhost:8080"},
		},
		{
			name:     "unknown agent",
			host:     "web.nonexistent.localhost:8080",
			wantCode: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()

			rp.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("Host %q: status = %d, want %d", tt.host, rec.Code, tt.wantCode)
			}

			if tt.wantBody != "" || len(tt.wantBodyContains) > 0 {
				body, _ := io.ReadAll(rec.Body)
				if tt.wantBody != "" && string(body) != tt.wantBody {
					t.Errorf("Host %q: body = %q, want %q", tt.host, body, tt.wantBody)
				}
				for _, want := range tt.wantBodyContains {
					if !strings.Contains(string(body), want) {
						t.Errorf("Host %q: body missing %q\n%s", tt.host, want, body)
					}
				}
			}
		})
	}
}

// TestMultiEndpointTLS tests the full multi-endpoint scenario through the TLS
// proxy server, matching how browsers access endpoints via HTTPS.
func TestMultiEndpointTLS(t *testing.T) {
	webBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("web endpoint"))
	}))
	defer webBackend.Close()

	apiBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("api endpoint"))
	}))
	defer apiBackend.Close()

	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	routes.Add("demo", map[string]string{
		"web": webBackend.Listener.Addr().String(),
		"api": apiBackend.Listener.Addr().String(),
	})

	caDir := t.TempDir()
	ca, err := proxy.NewCA(caDir)
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}

	ps := NewProxyServer(routes)
	if err := ps.EnableTLS(ca); err != nil {
		t.Fatalf("EnableTLS: %v", err)
	}
	if err := ps.Start(0); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ps.Stop(context.Background())

	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(ca.CertPEM())

	makeClient := func(serverName string) *http.Client {
		return &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					RootCAs:    certPool,
					ServerName: serverName,
					MinVersion: tls.VersionTLS12,
				},
			},
		}
	}

	proxyAddr := fmt.Sprintf("127.0.0.1:%d", ps.Port())

	// Test HTTPS to web endpoint
	t.Run("https web endpoint", func(t *testing.T) {
		client := makeClient("web.demo.localhost")
		req, _ := http.NewRequest("GET", "https://"+proxyAddr+"/", nil)
		req.Host = "web.demo.localhost"
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("HTTPS request: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "web endpoint" {
			t.Errorf("Body = %q, want 'web endpoint'", body)
		}
	})

	// Test HTTPS to api endpoint
	t.Run("https api endpoint", func(t *testing.T) {
		client := makeClient("api.demo.localhost")
		req, _ := http.NewRequest("GET", "https://"+proxyAddr+"/", nil)
		req.Host = "api.demo.localhost"
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("HTTPS request: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "api endpoint" {
			t.Errorf("Body = %q, want 'api endpoint'", body)
		}
	})

	// Test HTTP (plain) to web endpoint
	t.Run("http web endpoint", func(t *testing.T) {
		client := &http.Client{}
		req, _ := http.NewRequest("GET", "http://"+proxyAddr+"/", nil)
		req.Host = "web.demo.localhost"
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("HTTP request: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "web endpoint" {
			t.Errorf("Body = %q, want 'web endpoint'", body)
		}
	})

	// Test HTTP (plain) to api endpoint
	t.Run("http api endpoint", func(t *testing.T) {
		client := &http.Client{}
		req, _ := http.NewRequest("GET", "http://"+proxyAddr+"/", nil)
		req.Host = "api.demo.localhost"
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("HTTP request: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "api endpoint" {
			t.Errorf("Body = %q, want 'api endpoint'", body)
		}
	})

	// Test with port in Host header (as browsers send for non-standard ports)
	t.Run("host with port", func(t *testing.T) {
		client := makeClient("web.demo.localhost")
		req, _ := http.NewRequest("GET", "https://"+proxyAddr+"/", nil)
		req.Host = fmt.Sprintf("web.demo.localhost:%d", ps.Port())
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("HTTPS request with port: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "web endpoint" {
			t.Errorf("Body = %q, want 'web endpoint'", body)
		}
	})
}

// TestMultiAgentRouting verifies that multiple agents with the same endpoint
// names route to separate backends, as described in the ports documentation.
func TestMultiAgentRouting(t *testing.T) {
	darkModeBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("dark-mode"))
	}))
	defer darkModeBackend.Close()

	checkoutBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("checkout"))
	}))
	defer checkoutBackend.Close()

	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	routes.Add("dark-mode", map[string]string{
		"web": darkModeBackend.Listener.Addr().String(),
	})
	routes.Add("checkout", map[string]string{
		"web": checkoutBackend.Listener.Addr().String(),
	})

	rp := NewReverseProxy(routes)

	tests := []struct {
		host     string
		wantBody string
	}{
		{"web.dark-mode.localhost:8080", "dark-mode"},
		{"web.checkout.localhost:8080", "checkout"},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()
			rp.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("Status = %d, want 200", rec.Code)
			}
			body, _ := io.ReadAll(rec.Body)
			if string(body) != tt.wantBody {
				t.Errorf("Body = %q, want %q", body, tt.wantBody)
			}
		})
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
