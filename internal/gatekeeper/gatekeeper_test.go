package gatekeeper

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// waitForProxy polls until the server's proxy is accepting connections.
func waitForProxy(t *testing.T, srv *Server, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		addr := srv.ProxyAddr()
		if addr != "" {
			conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
			if err == nil {
				conn.Close()
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("proxy did not start in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestServerStartStop(t *testing.T) {
	cfg := &Config{
		Proxy: ProxyConfig{
			Port: 0, // ephemeral
			Host: "127.0.0.1",
		},
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	waitForProxy(t, srv, 2*time.Second)

	// Stop
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after cancel")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if err := srv.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestServerHealthEndpoint(t *testing.T) {
	cfg := &Config{
		Proxy: ProxyConfig{Port: 0, Host: "127.0.0.1"},
	}
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	waitForProxy(t, srv, 2*time.Second)

	resp, err := http.Get("http://" + srv.ProxyAddr() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status: %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"ok"`) {
		t.Errorf("healthz body = %s, want JSON with ok", body)
	}

	cancel()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if err := srv.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestCredentials(t *testing.T) {
	t.Setenv("TEST_GH_TOKEN", "ghp_test_single_tenant")

	cfg := &Config{
		Proxy: ProxyConfig{Port: 0, Host: "127.0.0.1"},
		Credentials: []CredentialConfig{
			{
				Host:   "api.github.com",
				Header: "Authorization",
				Grant:  "github",
				Source: SourceConfig{Type: "env", Var: "TEST_GH_TOKEN"},
			},
			{
				Host:   "api.anthropic.com",
				Header: "x-api-key",
				Source: SourceConfig{Type: "static", Value: "sk-ant-test"},
			},
		},
		Network: NetworkConfig{Policy: "permissive"},
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// No context resolver — credentials are injected for all matching requests.
	_, ok := srv.proxy.ResolveContext("any-token")
	if ok {
		t.Error("expected no context resolver in static credentials mode")
	}
}

func TestDefaultHeader(t *testing.T) {
	t.Setenv("TEST_TOKEN", "Bearer test123")

	cfg := &Config{
		Proxy: ProxyConfig{Port: 0, Host: "127.0.0.1"},
		Credentials: []CredentialConfig{
			{
				Host:   "api.example.com",
				Source: SourceConfig{Type: "env", Var: "TEST_TOKEN"},
			},
		},
	}

	// Should succeed — header defaults to "Authorization" when omitted.
	_, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
}

func TestMissingHost(t *testing.T) {
	cfg := &Config{
		Proxy: ProxyConfig{Port: 0, Host: "127.0.0.1"},
		Credentials: []CredentialConfig{
			{
				Source: SourceConfig{Type: "static", Value: "test"},
			},
		},
	}

	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for credential without host")
	}
	if !strings.Contains(err.Error(), "host is required") {
		t.Errorf("error = %q, want 'host is required'", err)
	}
}

func TestAuthToken(t *testing.T) {
	cfg := &Config{
		Proxy: ProxyConfig{
			Port:      0,
			Host:      "127.0.0.1",
			AuthToken: "my-secret-token",
		},
		Credentials: []CredentialConfig{
			{
				Host:   "api.example.com",
				Source: SourceConfig{Type: "static", Value: "test-cred"},
			},
		},
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	waitForProxy(t, srv, 2*time.Second)

	// Request without auth token should be rejected.
	req, _ := http.NewRequest(http.MethodGet, "http://"+srv.ProxyAddr(), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET without auth: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Errorf("no-auth status = %d, want %d", resp.StatusCode, http.StatusProxyAuthRequired)
	}
}

func TestDefaultProxyHost(t *testing.T) {
	cfg := &Config{
		Proxy: ProxyConfig{Port: 0}, // no host specified
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Start(ctx) }()
	waitForProxy(t, srv, 2*time.Second)

	// Should have bound to 127.0.0.1.
	addr := srv.ProxyAddr()
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Errorf("proxy addr = %q, want 127.0.0.1:*", addr)
	}

	cancel()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	srv.Stop(stopCtx)
}
