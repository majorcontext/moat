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
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/majorcontext/moat/internal/proxy"
)

// defaultProxyHost is the default bind address when none is configured.
// Binding to localhost prevents accidental exposure on all interfaces.
const defaultProxyHost = "127.0.0.1"

// configureLogging sets up slog based on the LogConfig.
// Returns a cleanup function to close any opened log file and an error.
func configureLogging(cfg LogConfig) (func(), error) {
	var level slog.Level
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	var (
		w       *os.File
		cleanup func()
	)
	switch strings.ToLower(cfg.Output) {
	case "", "stderr":
		w = os.Stderr
	case "stdout":
		w = os.Stdout
	default:
		f, err := os.OpenFile(cfg.Output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("opening log output %q: %w", cfg.Output, err)
		}
		w = f
		cleanup = func() { f.Close() }
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if strings.ToLower(cfg.Format) == "json" {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}
	slog.SetDefault(slog.New(handler))
	return cleanup, nil
}

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
	logCleanup  func() // closes log file if output is a file path

	mu      sync.Mutex
	started bool
}

// New creates a new Gate Keeper server from the given configuration.
// The context is used for credential fetching (e.g., AWS Secrets Manager)
// and can be used to cancel startup if the process receives a signal.
func New(ctx context.Context, cfg *Config) (*Server, error) {
	// Configure structured logging before anything else.
	logCleanup, err := configureLogging(cfg.Log)
	if err != nil {
		return nil, err
	}

	p := proxy.NewProxy()

	s := &Server{
		proxy:      p,
		logCleanup: logCleanup,
		cfg:        cfg,
	}

	// Load TLS CA for HTTPS interception. Without a CA, the proxy cannot
	// inject credentials into HTTPS requests (CONNECT tunnels pass through).
	if cfg.TLS.CACert != "" && cfg.TLS.CAKey != "" {
		certPEM, err := os.ReadFile(cfg.TLS.CACert)
		if err != nil {
			return nil, fmt.Errorf("reading CA cert: %w", err)
		}
		keyPEM, err := os.ReadFile(cfg.TLS.CAKey)
		if err != nil {
			return nil, fmt.Errorf("reading CA key: %w", err)
		}
		ca, err := proxy.LoadCA(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("loading CA: %w", err)
		}
		p.SetCA(ca)
	}

	// Load credentials from config and set directly on the proxy.
	// Credentials are fetched once at startup. For sources like
	// aws-secretsmanager, restart the process to pick up rotated values.
	if err := s.loadCredentials(ctx, cfg); err != nil {
		return nil, fmt.Errorf("loading credentials: %w", err)
	}

	// Set up request logging so proxied requests are visible.
	p.SetLogger(func(data proxy.RequestLogData) {
		attrs := []slog.Attr{
			slog.String("method", data.Method),
			slog.String("url", data.URL),
			slog.Int("status", data.StatusCode),
			slog.String("duration", data.Duration.Round(time.Millisecond).String()),
		}
		if data.AuthInjected {
			attrs = append(attrs, slog.Bool("credential_injected", true))
		}
		if data.Err != nil {
			attrs = append(attrs, slog.String("error", data.Err.Error()))
		}
		args := make([]any, len(attrs))
		for i, a := range attrs {
			args[i] = a
		}
		slog.Info("request", args...)
	})

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
func (s *Server) loadCredentials(ctx context.Context, cfg *Config) error {
	for _, cred := range cfg.Credentials {
		if cred.Host == "" {
			return fmt.Errorf("credential %q: host is required", cred.Grant)
		}

		src, err := ResolveSource(cred.Source)
		if err != nil {
			return fmt.Errorf("credential for %s: %w", cred.Host, err)
		}

		fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		val, err := src.Fetch(fetchCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("credential for %s: fetch failed: %w", cred.Host, err)
		}

		header := cred.Header
		if header == "" {
			header = "Authorization"
		}

		// For Authorization headers, ensure the value includes an auth
		// scheme prefix. In the CLI flow, providers handle this (e.g.,
		// GitHub provider prepends "Bearer "). The gatekeeper bypasses
		// providers, so we auto-detect the scheme from the token format.
		if strings.EqualFold(header, "Authorization") {
			val = ensureAuthScheme(val, cred.Prefix)
		}

		s.proxy.SetCredentialWithGrant(cred.Host, header, val, cred.Grant)
	}
	return nil
}

// ensureAuthScheme ensures a credential value has an auth scheme prefix
// suitable for an Authorization header. If the value already contains a
// scheme (e.g., "Bearer xxx", "token xxx"), it is returned unchanged.
// If prefix is set explicitly, it is used. Otherwise the scheme is
// auto-detected from known GitHub token prefixes, defaulting to "Bearer".
func ensureAuthScheme(val, prefix string) string {
	// If the value already has a scheme prefix, leave it alone.
	// Auth schemes are a single token followed by a space (RFC 7235).
	if i := strings.IndexByte(val, ' '); i > 0 {
		scheme := val[:i]
		// Looks like "Bearer xxx" or "token xxx" — already prefixed.
		if isAuthScheme(scheme) {
			return val
		}
	}

	if prefix != "" {
		return prefix + " " + val
	}

	// Auto-detect from known GitHub token prefixes.
	switch {
	case strings.HasPrefix(val, "ghp_"), strings.HasPrefix(val, "ghs_"):
		// Classic PAT and App installation tokens use "token" scheme.
		return "token " + val
	case strings.HasPrefix(val, "gho_"), strings.HasPrefix(val, "github_pat_"):
		// OAuth and fine-grained PAT tokens use "Bearer" scheme.
		return "Bearer " + val
	default:
		return "Bearer " + val
	}
}

// isAuthScheme returns true if s looks like a valid HTTP auth scheme.
// Auth schemes start with a letter and contain only letters, digits, hyphens,
// and underscores (token68 subset per RFC 7235).
func isAuthScheme(s string) bool {
	if len(s) == 0 {
		return false
	}
	// Must start with a letter.
	c := s[0]
	if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
		return false
	}
	for _, c := range s[1:] {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
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

	slog.Info("gatekeeper listening", "addr", ln.Addr().String())

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
	if s.logCleanup != nil {
		defer s.logCleanup()
	}
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
