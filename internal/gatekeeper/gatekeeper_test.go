package gatekeeper

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/proxy"
)

// startTLSBackend creates an HTTPS backend server using the provided CA.
// Returns the server (for cleanup), the port it's listening on, and the CA cert pool.
func startTLSBackend(t *testing.T, ca *proxy.CA, handler http.Handler) (srv *http.Server, port string, caCertPool *x509.CertPool) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.GenerateCert("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	tlsLn := tls.NewListener(ln, &tls.Config{
		Certificates: []tls.Certificate{*cert},
		MinVersion:   tls.VersionTLS12,
	})
	srv = &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(tlsLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("backend serve: %v", err)
		}
	}()
	t.Cleanup(func() { srv.Close() })

	_, port, _ = net.SplitHostPort(ln.Addr().String())
	caCertPool = x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(ca.CertPEM())
	return srv, port, caCertPool
}

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

	srv, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	waitForProxy(t, srv, 2*time.Second)

	// Canceling the context causes Start() to call Stop() internally.
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after cancel")
	}
}

func TestServerHealthEndpoint(t *testing.T) {
	cfg := &Config{
		Proxy: ProxyConfig{Port: 0, Host: "127.0.0.1"},
	}
	srv, err := New(context.Background(), cfg)
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

	// Canceling the context causes Start() to call Stop() internally.
	cancel()
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

	srv, err := New(context.Background(), cfg)
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
	_, err := New(context.Background(), cfg)
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

	_, err := New(context.Background(), cfg)
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

	srv, err := New(context.Background(), cfg)
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

	srv, err := New(context.Background(), cfg)
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
	if err := srv.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestTLSCALoading(t *testing.T) {
	// Generate a CA and verify it's actually used for HTTPS interception
	// by sending a request through the proxy.
	dir := t.TempDir()
	ca, err := proxy.NewCA(dir)
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}

	// Start an HTTPS backend signed by this CA.
	backendLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	backendCert, err := ca.GenerateCert("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	backendSrv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		ReadHeaderTimeout: 5 * time.Second,
	}
	tlsLn := tls.NewListener(backendLn, &tls.Config{
		Certificates: []tls.Certificate{*backendCert},
		MinVersion:   tls.VersionTLS12,
	})
	go func() {
		if err := backendSrv.Serve(tlsLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("backend serve: %v", err)
		}
	}()
	defer backendSrv.Close()
	_, backendPort, _ := net.SplitHostPort(backendLn.Addr().String())

	cfg := &Config{
		Proxy:   ProxyConfig{Port: 0, Host: "127.0.0.1"},
		TLS:     TLSConfig{CACert: filepath.Join(dir, "ca.crt"), CAKey: filepath.Join(dir, "ca.key")},
		Network: NetworkConfig{Policy: "permissive"},
	}

	srv, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New with TLS: %v", err)
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(ca.CertPEM())
	srv.proxy.SetUpstreamCAs(caCertPool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	waitForProxy(t, srv, 2*time.Second)

	// If the CA loaded correctly, the proxy can MITM-intercept HTTPS.
	proxyURL, _ := url.Parse("http://" + srv.ProxyAddr())
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: caCertPool, MinVersion: tls.VersionTLS12},
		},
	}

	resp, err := client.Get("https://127.0.0.1:" + backendPort + "/tls-check")
	if err != nil {
		t.Fatalf("HTTPS through proxy failed (CA not loaded?): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestTLSCAMissingCert(t *testing.T) {
	cfg := &Config{
		Proxy: ProxyConfig{Port: 0, Host: "127.0.0.1"},
		TLS: TLSConfig{
			CACert: "/nonexistent/ca.crt",
			CAKey:  "/nonexistent/ca.key",
		},
	}

	_, err := New(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for missing CA cert")
	}
	if !strings.Contains(err.Error(), "reading CA cert") {
		t.Errorf("error = %q, want to mention 'reading CA cert'", err)
	}
}

func TestTLSCAPartialConfig(t *testing.T) {
	// Only CACert set, no CAKey — should create server without TLS (no error).
	cfg := &Config{
		Proxy: ProxyConfig{Port: 0, Host: "127.0.0.1"},
		TLS: TLSConfig{
			CACert: "/some/cert.pem",
		},
	}

	_, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New with partial TLS should succeed (no interception): %v", err)
	}
}

func TestTLSCAWithECKey(t *testing.T) {
	// gen-ca.sh generates EC keys (prime256v1). Verify they load correctly.
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating EC key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test EC CA"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating cert: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshaling key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		Proxy: ProxyConfig{Port: 0, Host: "127.0.0.1"},
		TLS: TLSConfig{
			CACert: certPath,
			CAKey:  keyPath,
		},
	}

	_, err = New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New with EC CA: %v", err)
	}
}

func TestHTTPSCredentialInjection(t *testing.T) {
	// End-to-end test: start gatekeeper with TLS + credential,
	// send HTTPS request through proxy, verify credential was injected.

	caDir := t.TempDir()
	ca, err := proxy.NewCA(caDir)
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}

	var (
		gotAuth string
		authMu  sync.Mutex
	)
	_, backendPort, caCertPool := startTLSBackend(t, ca, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authMu.Lock()
		gotAuth = r.Header.Get("Authorization")
		authMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))

	cfg := &Config{
		Proxy: ProxyConfig{Port: 0, Host: "127.0.0.1"},
		TLS: TLSConfig{
			CACert: filepath.Join(caDir, "ca.crt"),
			CAKey:  filepath.Join(caDir, "ca.key"),
		},
		Credentials: []CredentialConfig{
			{
				Host:   "127.0.0.1",
				Header: "Authorization",
				Grant:  "test",
				Source: SourceConfig{Type: "static", Value: "Bearer secret123"},
			},
		},
		Network: NetworkConfig{Policy: "permissive"},
	}

	srv, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// The proxy's upstream transport must trust the test CA that signed the
	// backend's cert. In production, upstream certs are signed by public CAs
	// in the system roots. In this test, the backend uses a cert from our
	// test CA.
	srv.proxy.SetUpstreamCAs(caCertPool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	waitForProxy(t, srv, 2*time.Second)

	proxyURL, _ := url.Parse("http://" + srv.ProxyAddr())
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: caCertPool, MinVersion: tls.VersionTLS12},
		},
	}

	resp, err := client.Get("https://127.0.0.1:" + backendPort + "/test")
	if err != nil {
		t.Fatalf("GET through proxy: %v", err)
	}
	resp.Body.Close()

	authMu.Lock()
	auth := gotAuth
	authMu.Unlock()
	if auth != "Bearer secret123" {
		t.Errorf("backend got Authorization = %q, want %q", auth, "Bearer secret123")
	}
}

func TestTLSCAInvalidPEM(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")
	if err := os.WriteFile(certPath, []byte("not a cert"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("not a key"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		Proxy: ProxyConfig{Port: 0, Host: "127.0.0.1"},
		TLS: TLSConfig{
			CACert: certPath,
			CAKey:  keyPath,
		},
	}

	_, err := New(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
	if !strings.Contains(err.Error(), "loading CA") {
		t.Errorf("error = %q, want to mention 'loading CA'", err)
	}
}

func TestHTTPCredentialInjection(t *testing.T) {
	// End-to-end test for the plain HTTP (non-CONNECT) path.
	// The proxy intercepts HTTP requests and injects credentials directly.

	// Start an HTTP backend that echoes the Authorization header.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Header.Get("Authorization"))
	}))
	defer backend.Close()

	backendURL, _ := url.Parse(backend.URL)

	cfg := &Config{
		Proxy: ProxyConfig{Port: 0, Host: "127.0.0.1"},
		Credentials: []CredentialConfig{
			{
				Host:   backendURL.Hostname(),
				Grant:  "test",
				Source: SourceConfig{Type: "static", Value: "Bearer http-secret"},
			},
		},
		Network: NetworkConfig{Policy: "permissive"},
	}

	srv, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	waitForProxy(t, srv, 2*time.Second)

	proxyURL, _ := url.Parse("http://" + srv.ProxyAddr())
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}

	resp, err := client.Get(backend.URL + "/test")
	if err != nil {
		t.Fatalf("GET through proxy: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if string(body) != "Bearer http-secret" {
		t.Errorf("backend got Authorization = %q, want %q", body, "Bearer http-secret")
	}
}

func TestHTTPSCustomHeaderInjection(t *testing.T) {
	// Test injection of a custom header (x-api-key) via HTTPS CONNECT path.
	caDir := t.TempDir()
	ca, err := proxy.NewCA(caDir)
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}

	var (
		gotHeader string
		mu        sync.Mutex
	)
	_, backendPort, caCertPool := startTLSBackend(t, ca, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotHeader = r.Header.Get("x-api-key")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))

	cfg := &Config{
		Proxy: ProxyConfig{Port: 0, Host: "127.0.0.1"},
		TLS: TLSConfig{
			CACert: filepath.Join(caDir, "ca.crt"),
			CAKey:  filepath.Join(caDir, "ca.key"),
		},
		Credentials: []CredentialConfig{
			{
				Host:   "127.0.0.1",
				Header: "x-api-key",
				Grant:  "anthropic",
				Source: SourceConfig{Type: "static", Value: "sk-ant-test123"},
			},
		},
		Network: NetworkConfig{Policy: "permissive"},
	}

	srv, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	srv.proxy.SetUpstreamCAs(caCertPool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	waitForProxy(t, srv, 2*time.Second)

	proxyURL, _ := url.Parse("http://" + srv.ProxyAddr())
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: caCertPool, MinVersion: tls.VersionTLS12},
		},
	}

	resp, err := client.Get("https://127.0.0.1:" + backendPort + "/test")
	if err != nil {
		t.Fatalf("GET through proxy: %v", err)
	}
	resp.Body.Close()

	mu.Lock()
	got := gotHeader
	mu.Unlock()
	if got != "sk-ant-test123" {
		t.Errorf("backend got x-api-key = %q, want %q", got, "sk-ant-test123")
	}
}

func TestStrictNetworkPolicy(t *testing.T) {
	// Verify that strict network policy blocks requests to disallowed hosts
	// and allows requests to allowed hosts.

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	backendURL, _ := url.Parse(backend.URL)

	cfg := &Config{
		Proxy: ProxyConfig{Port: 0, Host: "127.0.0.1"},
		Network: NetworkConfig{
			Policy: "strict",
			// Allow the backend host:port — httptest servers run on high ports.
			Allow: []string{backendURL.Host},
		},
	}

	srv, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	waitForProxy(t, srv, 2*time.Second)

	proxyURL, _ := url.Parse("http://" + srv.ProxyAddr())
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}

	// Allowed host should succeed.
	resp, err := client.Get(backend.URL + "/allowed")
	if err != nil {
		t.Fatalf("GET allowed host: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("allowed host status = %d, want 200", resp.StatusCode)
	}
	if string(body) != "ok" {
		t.Errorf("allowed host body = %q, want %q", body, "ok")
	}

	// Blocked host should fail. The proxy intentionally returns 407 (Proxy
	// Authentication Required) for network policy denials rather than 403.
	// This is by design: HTTP clients behind a proxy expect 407 for proxy-level
	// rejections, and 403 would imply the origin server denied the request.
	resp, err = client.Get("http://blocked.example.com/denied")
	if err != nil {
		t.Fatalf("GET blocked host: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Errorf("blocked host status = %d, want 407", resp.StatusCode)
	}
}

func TestMultipleCredentialsSameHost(t *testing.T) {
	// Multiple credentials for the same host (Authorization + x-api-key).
	// Both should be injected into the same request.

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Header.Get("Authorization")+"|"+r.Header.Get("x-api-key"))
	}))
	defer backend.Close()

	backendURL, _ := url.Parse(backend.URL)

	cfg := &Config{
		Proxy: ProxyConfig{Port: 0, Host: "127.0.0.1"},
		Credentials: []CredentialConfig{
			{
				Host:   backendURL.Hostname(),
				Grant:  "github",
				Source: SourceConfig{Type: "static", Value: "Bearer gh-token"},
			},
			{
				Host:   backendURL.Hostname(),
				Header: "x-api-key",
				Grant:  "anthropic",
				Source: SourceConfig{Type: "static", Value: "sk-ant-key"},
			},
		},
		Network: NetworkConfig{Policy: "permissive"},
	}

	srv, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	waitForProxy(t, srv, 2*time.Second)

	proxyURL, _ := url.Parse("http://" + srv.ProxyAddr())
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}

	resp, err := client.Get(backend.URL + "/test")
	if err != nil {
		t.Fatalf("GET through proxy: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if string(body) != "Bearer gh-token|sk-ant-key" {
		t.Errorf("backend got %q, want %q", body, "Bearer gh-token|sk-ant-key")
	}
}

func TestAuthSchemeAutoDetectionThroughProxy(t *testing.T) {
	// Verify that a bare GitHub PAT gets "token" prefix when injected.

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Header.Get("Authorization"))
	}))
	defer backend.Close()

	backendURL, _ := url.Parse(backend.URL)

	cfg := &Config{
		Proxy: ProxyConfig{Port: 0, Host: "127.0.0.1"},
		Credentials: []CredentialConfig{
			{
				Host:  backendURL.Hostname(),
				Grant: "github",
				// Bare GitHub classic PAT — should auto-detect "token" prefix.
				Source: SourceConfig{Type: "static", Value: "ghp_abc123def456"},
			},
		},
		Network: NetworkConfig{Policy: "permissive"},
	}

	srv, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	waitForProxy(t, srv, 2*time.Second)

	proxyURL, _ := url.Parse("http://" + srv.ProxyAddr())
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}

	resp, err := client.Get(backend.URL + "/repos")
	if err != nil {
		t.Fatalf("GET through proxy: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	want := "token ghp_abc123def456"
	if string(body) != want {
		t.Errorf("backend got Authorization = %q, want %q", body, want)
	}
}

func TestEnsureAuthScheme(t *testing.T) {
	tests := []struct {
		name   string
		val    string
		prefix string
		want   string
	}{
		// Already prefixed — pass through unchanged.
		{name: "bearer already", val: "Bearer gho_abc", prefix: "", want: "Bearer gho_abc"},
		{name: "token already", val: "token ghp_abc", prefix: "", want: "token ghp_abc"},
		{name: "basic already", val: "Basic dXNlcjpwYXNz", prefix: "", want: "Basic dXNlcjpwYXNz"},

		// Explicit prefix overrides auto-detection.
		{name: "explicit prefix", val: "ghp_abc", prefix: "token", want: "token ghp_abc"},
		{name: "explicit Bearer", val: "sk-xxx", prefix: "Bearer", want: "Bearer sk-xxx"},
		{name: "explicit ApiKey", val: "key123", prefix: "ApiKey", want: "ApiKey key123"},
		// Explicit prefix does not double-prefix if value already has scheme.
		{name: "explicit prefix with existing scheme", val: "Bearer gho_abc", prefix: "token", want: "Bearer gho_abc"},

		// GitHub auto-detection.
		{name: "ghp classic PAT", val: "ghp_abc123", prefix: "", want: "token ghp_abc123"},
		{name: "ghs app token", val: "ghs_abc123", prefix: "", want: "token ghs_abc123"},
		{name: "gho OAuth", val: "gho_abc123", prefix: "", want: "Bearer gho_abc123"},
		{name: "github_pat fine-grained", val: "github_pat_abc123", prefix: "", want: "Bearer github_pat_abc123"},

		// Unknown tokens default to Bearer.
		{name: "unknown token", val: "sk-ant-abc123", prefix: "", want: "Bearer sk-ant-abc123"},
		{name: "opaque token", val: "abc123def456", prefix: "", want: "Bearer abc123def456"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ensureAuthScheme(tt.val, tt.prefix)
			if got != tt.want {
				t.Errorf("ensureAuthScheme(%q, %q) = %q, want %q", tt.val, tt.prefix, got, tt.want)
			}
		})
	}
}
