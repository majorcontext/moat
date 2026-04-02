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
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/proxy"
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
	srv.Stop(stopCtx)
}

func TestTLSCALoading(t *testing.T) {
	// Generate a CA to test loading.
	dir := t.TempDir()
	ca, err := proxy.NewCA(dir)
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	_ = ca

	cfg := &Config{
		Proxy: ProxyConfig{Port: 0, Host: "127.0.0.1"},
		TLS: TLSConfig{
			CACert: filepath.Join(dir, "ca.crt"),
			CAKey:  filepath.Join(dir, "ca.key"),
		},
	}

	srv, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New with TLS: %v", err)
	}
	// Verify the proxy has a CA set (it should be able to intercept HTTPS).
	if srv.proxy == nil {
		t.Fatal("proxy is nil")
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

	// 1. Generate CA via proxy.NewCA (RSA).
	caDir := t.TempDir()
	ca, err := proxy.NewCA(caDir)
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}

	// 2. Start an HTTPS backend that records the Authorization header.
	var (
		gotAuth string
		authMu  sync.Mutex
	)
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authMu.Lock()
		gotAuth = r.Header.Get("Authorization")
		authMu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	backendLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// Backend uses a real TLS cert. The proxy will MITM and present its own,
	// so the backend's cert doesn't need to be trusted by the client.
	backendCert, err := ca.GenerateCert("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	tlsLn := tls.NewListener(backendLn, &tls.Config{
		Certificates: []tls.Certificate{*backendCert},
		MinVersion:   tls.VersionTLS12,
	})
	backendSrv := &http.Server{Handler: backend, ReadHeaderTimeout: 5 * time.Second}
	go backendSrv.Serve(tlsLn)
	defer backendSrv.Close()

	_, backendPort, _ := net.SplitHostPort(backendLn.Addr().String())

	// 3. Start gatekeeper with the CA and a static credential for 127.0.0.1.
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
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(ca.CertPEM())
	srv.proxy.SetUpstreamCAs(caCertPool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	waitForProxy(t, srv, 2*time.Second)

	// 4. Send HTTPS request through the proxy.
	proxyURL, _ := url.Parse("http://" + srv.ProxyAddr())

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{
				RootCAs:    caCertPool,
				MinVersion: tls.VersionTLS12,
			},
		},
	}

	targetURL := "https://127.0.0.1:" + backendPort + "/test"
	resp, err := client.Get(targetURL)
	if err != nil {
		t.Fatalf("GET through proxy: %v", err)
	}
	resp.Body.Close()

	// 5. Verify the credential was injected.
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
	os.WriteFile(certPath, []byte("not a cert"), 0644)
	os.WriteFile(keyPath, []byte("not a key"), 0644)

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
