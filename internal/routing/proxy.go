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
	"time"

	"github.com/andybons/moat/internal/log"
	"github.com/andybons/moat/internal/proxy"
)

// ReverseProxy routes requests based on Host header to container services.
type ReverseProxy struct {
	routes *RouteTable
}

// NewReverseProxy creates a reverse proxy with the given route table.
func NewReverseProxy(routes *RouteTable) *ReverseProxy {
	return &ReverseProxy{routes: routes}
}

// ServeHTTP handles incoming requests and routes them to backends.
func (rp *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Parse Host header: [service.]agent.localhost[:port]
	host := r.Host
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx] // Remove port
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
