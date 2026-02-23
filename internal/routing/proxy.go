package routing

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/proxy"
)

// ReverseProxy routes requests based on Host header to container services.
type ReverseProxy struct {
	routes        *RouteTable
	oauthMu       sync.RWMutex
	oauthHandler  http.Handler // optional OAuth relay handler
	oauthHostname string       // hostname without port (e.g. "oauthrelay.localhost")
}

// NewReverseProxy creates a reverse proxy with the given route table.
func NewReverseProxy(routes *RouteTable) *ReverseProxy {
	return &ReverseProxy{routes: routes}
}

// SetOAuthRelay registers an OAuth relay handler. Requests to the given hostname
// (e.g. "oauthrelay.localhost") are routed to this handler instead of the normal
// agent routing logic.
func (rp *ReverseProxy) SetOAuthRelay(hostname string, handler http.Handler) {
	// Strip port from hostname if present
	if idx := strings.LastIndex(hostname, ":"); idx != -1 {
		hostname = hostname[:idx]
	}
	rp.oauthMu.Lock()
	rp.oauthHostname = hostname
	rp.oauthHandler = handler
	rp.oauthMu.Unlock()
}

// ServeHTTP handles incoming requests and routes them to backends.
func (rp *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Parse Host header: [service.]agent.localhost[:port]
	host := r.Host
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx] // Remove port
	}

	// Check for OAuth relay requests before normal routing
	rp.oauthMu.RLock()
	oauthHandler := rp.oauthHandler
	oauthHostname := rp.oauthHostname
	rp.oauthMu.RUnlock()
	if oauthHandler != nil && host == oauthHostname {
		oauthHandler.ServeHTTP(w, r)
		return
	}

	// Remove .localhost suffix
	host = strings.TrimSuffix(host, ".localhost")

	parts := strings.SplitN(host, ".", 2)
	var service, agent string
	if len(parts) == 2 {
		service = parts[0]
		agent = parts[1]
	} else {
		// No service prefix, just agent name
		agent = parts[0]
	}

	// Lookup backend address
	var backendAddr string
	var ok bool
	if service != "" {
		backendAddr, ok = rp.routes.Lookup(agent, service)
	}
	if !ok {
		backendAddr, ok = rp.routes.LookupDefault(agent)
	}

	if !ok {
		registered := rp.routes.Agents()
		log.Debug("routing: unknown agent",
			"agent", agent,
			"service", service,
			"host", r.Host,
			"registered", registered,
		)
		rp.writeError(w, http.StatusNotFound, "unknown agent", agent)
		return
	}

	// Proxy the request
	target, err := url.Parse("http://" + backendAddr)
	if err != nil {
		rp.writeError(w, http.StatusInternalServerError, "invalid backend", backendAddr)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	// Wrap the default Director to add standard proxy headers.
	// httputil.NewSingleHostReverseProxy already sets X-Forwarded-For.
	defaultDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		defaultDirector(req)
		// Preserve original proto so backends behind TLS-terminating proxies
		// can detect the original scheme (needed for secure-context checks).
		if r.TLS != nil {
			req.Header.Set("X-Forwarded-Proto", "https")
		} else if req.Header.Get("X-Forwarded-Proto") == "" {
			req.Header.Set("X-Forwarded-Proto", "http")
		}
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		rp.writeError(w, http.StatusBadGateway, "service unavailable", err.Error())
	}

	proxy.ServeHTTP(w, r)
}

func (rp *ReverseProxy) writeError(w http.ResponseWriter, code int, errType, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":  errType,
		"detail": detail,
	})
}

// ProxyServer wraps the reverse proxy with lifecycle management.
type ProxyServer struct {
	rp        *ReverseProxy
	listener  net.Listener
	mux       *muxListener
	server    *http.Server
	port      int
	ca        *proxy.CA
	tlsConfig *tls.Config
}

// NewProxyServer creates a new proxy server.
func NewProxyServer(routes *RouteTable) *ProxyServer {
	return &ProxyServer{
		rp: NewReverseProxy(routes),
	}
}

// ReverseProxy returns the underlying reverse proxy for configuration.
func (ps *ProxyServer) ReverseProxy() *ReverseProxy {
	return ps.rp
}

// EnableTLS configures TLS support using the given CA.
// Must be called before Start().
func (ps *ProxyServer) EnableTLS(ca *proxy.CA) error {
	if ps.tlsConfig != nil {
		return fmt.Errorf("TLS already enabled")
	}
	if ps.listener != nil {
		return fmt.Errorf("cannot enable TLS after Start()")
	}

	ps.ca = ca
	ps.tlsConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Dynamically generate certificates based on SNI (Server Name Indication).
		// This allows us to create valid certs for any hostname pattern like
		// web.myapp.localhost without needing invalid multi-level wildcards.
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return ca.GenerateCert(hello.ServerName)
		},
	}
	return nil
}

// Start starts the proxy server on the given port.
// If TLS is enabled, the server handles both HTTP and HTTPS on the same port.
func (ps *ProxyServer) Start(port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	ps.listener = listener
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("unexpected listener address type: %T", listener.Addr())
	}
	ps.port = tcpAddr.Port

	if ps.tlsConfig != nil {
		// Use multiplexing listener for HTTP/HTTPS auto-detection
		ps.mux = newMuxListener(listener, ps.tlsConfig, ps.rp)
		go func() {
			if err := ps.mux.serve(); err != nil && err != net.ErrClosed {
				log.Debug("mux listener error", "error", err)
			}
		}()
	} else {
		// Plain HTTP only
		ps.server = &http.Server{
			Handler:           ps.rp,
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() { _ = ps.server.Serve(listener) }()
	}

	return nil
}

// Port returns the port the server is listening on.
func (ps *ProxyServer) Port() int {
	return ps.port
}

// Stop gracefully shuts down the proxy server.
func (ps *ProxyServer) Stop(ctx context.Context) error {
	if ps.listener == nil {
		return nil
	}

	// For non-TLS mode, use graceful shutdown
	if ps.server != nil {
		return ps.server.Shutdown(ctx)
	}

	// For TLS mode, close the listener (muxListener handles per-connection servers)
	return ps.listener.Close()
}
