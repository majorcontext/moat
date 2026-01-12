package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
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
	json.NewEncoder(w).Encode(map[string]string{
		"error":  errType,
		"detail": detail,
	})
}

// ProxyServer wraps the reverse proxy with lifecycle management.
type ProxyServer struct {
	rp       *ReverseProxy
	server   *http.Server
	listener net.Listener
	port     int
}

// NewProxyServer creates a new proxy server.
func NewProxyServer(routes *RouteTable) *ProxyServer {
	return &ProxyServer{
		rp: NewReverseProxy(routes),
	}
}

// Start starts the proxy server on the given port.
func (ps *ProxyServer) Start(port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	ps.listener = listener
	ps.port = listener.Addr().(*net.TCPAddr).Port
	ps.server = &http.Server{Handler: ps.rp}

	go ps.server.Serve(listener)
	return nil
}

// Port returns the port the server is listening on.
func (ps *ProxyServer) Port() int {
	return ps.port
}

// Stop gracefully shuts down the proxy server.
func (ps *ProxyServer) Stop(ctx context.Context) error {
	if ps.server == nil {
		return nil
	}
	return ps.server.Shutdown(ctx)
}
