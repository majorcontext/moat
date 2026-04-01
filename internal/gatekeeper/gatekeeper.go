// Package gatekeeper provides a standalone credential-injecting TLS proxy.
//
// Credentials are pre-configured in gatekeeper.yaml and injected for all
// proxied requests matching the host. Access control is via network policy
// (who can reach the proxy) and an optional static auth token.
//
// For per-caller credential isolation (run registration, token-scoped
// credentials), use the daemon package, which provides a management API
// over a Unix socket.
package gatekeeper

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/majorcontext/moat/internal/proxy"
)

// defaultProxyHost is the default bind address when none is configured.
// Binding to localhost prevents accidental exposure on all interfaces.
const defaultProxyHost = "127.0.0.1"

// healthHandler wraps an HTTP handler to add a /healthz endpoint on the proxy port.
type healthHandler struct {
	next http.Handler
}

func (h *healthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return
	}
	h.next.ServeHTTP(w, r)
}

// Server is the Gate Keeper server. It manages a TLS-intercepting proxy
// with statically configured credentials.
type Server struct {
	proxy *proxy.Proxy
	cfg   *Config

	proxyAddr   string // actual address after Start
	proxyLn     net.Listener
	proxyServer *http.Server

	mu      sync.Mutex
	started bool
}

// New creates a new Gate Keeper server from the given configuration.
func New(cfg *Config) (*Server, error) {
	p := proxy.NewProxy()

	s := &Server{
		proxy: p,
		cfg:   cfg,
	}

	// Load credentials from config and set directly on the proxy.
	if err := s.loadCredentials(cfg); err != nil {
		return nil, fmt.Errorf("loading credentials: %w", err)
	}

	// Optional defense-in-depth: require a static token for proxy access.
	// Clients provide it via Proxy-Authorization header or
	// HTTP_PROXY=http://user:token@host.
	if cfg.Proxy.AuthToken != "" {
		p.SetAuthToken(cfg.Proxy.AuthToken)
	}

	// Configure network policy if specified.
	if cfg.Network.Policy != "" {
		p.SetNetworkPolicy(cfg.Network.Policy, cfg.Network.Allow, nil)
	}

	return s, nil
}

// loadCredentials resolves each credential from config and sets it on the proxy.
func (s *Server) loadCredentials(cfg *Config) error {
	for _, cred := range cfg.Credentials {
		if cred.Host == "" {
			return fmt.Errorf("credential %q: host is required", cred.Grant)
		}

		src, err := ResolveSource(cred.Source)
		if err != nil {
			return fmt.Errorf("credential for %s: %w", cred.Host, err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		val, err := src.Fetch(ctx)
		cancel()
		if err != nil {
			return fmt.Errorf("credential for %s: fetch failed: %w", cred.Host, err)
		}

		header := cred.Header
		if header == "" {
			header = "Authorization"
		}
		s.proxy.SetCredentialWithGrant(cred.Host, header, val, cred.Grant)
	}
	return nil
}

// Start starts the proxy. It blocks until the context is canceled.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("server already started")
	}
	s.started = true
	s.mu.Unlock()

	// Default to localhost if no host is configured.
	host := s.cfg.Proxy.Host
	if host == "" {
		host = defaultProxyHost
	}

	// Start proxy listener.
	addr := fmt.Sprintf("%s:%d", host, s.cfg.Proxy.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("starting proxy listener: %w", err)
	}

	s.mu.Lock()
	s.proxyLn = ln
	s.proxyAddr = ln.Addr().String()
	s.mu.Unlock()

	// Start proxy HTTP server with health check wrapper.
	s.proxyServer = &http.Server{
		Handler:           &healthHandler{next: s.proxy},
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WriteTimeout is intentionally omitted for the proxy server.
		// CONNECT tunnels are long-lived, and a write timeout would kill
		// idle but valid connections.
	}
	go func() { _ = s.proxyServer.Serve(ln) }()

	// Block until context canceled, then shut down.
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.Stop(shutdownCtx)
}

// Stop gracefully shuts down the proxy server.
func (s *Server) Stop(ctx context.Context) error {
	if s.proxyServer != nil {
		return s.proxyServer.Shutdown(ctx)
	}
	return nil
}

// ProxyAddr returns the proxy listener's actual address (host:port).
// Returns empty string if the proxy has not started.
func (s *Server) ProxyAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.proxyAddr
}
