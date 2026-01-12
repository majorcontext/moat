package proxy

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RequestLogger is called for each proxied request.
type RequestLogger func(method, url string, statusCode int, duration time.Duration, err error)

// Proxy is an HTTP proxy that injects credentials into outgoing requests.
//
// # Security Model
//
// The proxy handles two distinct security concerns:
//
//  1. Credential injection: The proxy injects Authorization headers for
//     configured hosts (e.g., api.github.com). When CA is set, it performs
//     TLS interception (MITM) to inject headers into HTTPS requests.
//
//  2. Proxy authentication: When authToken is set, clients must authenticate
//     to the proxy itself via Proxy-Authorization header. This prevents
//     unauthorized access when the proxy binds to all interfaces (0.0.0.0),
//     which is required for Apple containers that access the host via
//     gateway IP rather than localhost.
//
// For Docker containers, the proxy binds to localhost (127.0.0.1) and
// authentication is not required. For Apple containers, the proxy binds
// to all interfaces with a cryptographically secure token for authentication.
type Proxy struct {
	credentials map[string]string // host -> auth header value
	mu          sync.RWMutex
	ca          *CA           // Optional CA for TLS interception
	logger      RequestLogger // Optional request logger
	authToken   string        // Optional auth token required for proxy access
}

// NewProxy creates a new auth proxy.
func NewProxy() *Proxy {
	return &Proxy{
		credentials: make(map[string]string),
	}
}

// SetAuthToken sets the required authentication token for proxy access.
// When set, clients must include this token in the Proxy-Authorization header
// to use the proxy. Unauthenticated requests receive HTTP 407 (Proxy Auth Required).
//
// This is used for Apple containers where the proxy must bind to all interfaces
// (0.0.0.0) because containers access the host via gateway IP. The token prevents
// unauthorized network peers from accessing the credential-injecting proxy.
//
// Accepts both Basic auth (for HTTP_PROXY=http://user:token@host URLs that most
// HTTP clients support) and Bearer format. The token should be cryptographically
// random (e.g., 32 bytes from crypto/rand, hex-encoded to 64 characters).
func (p *Proxy) SetAuthToken(token string) {
	p.authToken = token
}

// SetCA sets the CA for TLS interception.
func (p *Proxy) SetCA(ca *CA) {
	p.ca = ca
}

// SetLogger sets the request logger.
func (p *Proxy) SetLogger(logger RequestLogger) {
	p.logger = logger
}

// SetCredential sets the credential for a host.
func (p *Proxy) SetCredential(host, authHeader string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.credentials[host] = authHeader
}

// hasCredential checks if there's a credential for the host.
func (p *Proxy) hasCredential(host string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	// Check with and without port
	if _, ok := p.credentials[host]; ok {
		return true
	}
	// Strip port and check
	h, _, _ := net.SplitHostPort(host)
	if h != "" {
		_, ok := p.credentials[h]
		return ok
	}
	return false
}

// getCredential returns the credential for a host.
func (p *Proxy) getCredential(host string) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if auth, ok := p.credentials[host]; ok {
		return auth, true
	}
	// Strip port and check
	h, _, _ := net.SplitHostPort(host)
	if h != "" {
		auth, ok := p.credentials[h]
		return auth, ok
	}
	return "", false
}

// ServeHTTP handles proxy requests.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Check authentication if token is required
	if p.authToken != "" {
		if !p.checkAuth(r) {
			http.Error(w, "Proxy authentication required", http.StatusProxyAuthRequired)
			return
		}
	}

	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleHTTP(w, r)
}

// checkAuth validates the Proxy-Authorization header against the required token.
// Accepts both Basic auth (from HTTP_PROXY=http://agentops:token@host) and Bearer format.
func (p *Proxy) checkAuth(r *http.Request) bool {
	auth := r.Header.Get("Proxy-Authorization")
	if auth == "" {
		return false
	}

	// Try Bearer format first: "Bearer <token>"
	if strings.HasPrefix(auth, "Bearer ") {
		return auth[7:] == p.authToken
	}

	// Try Basic format: "Basic <base64(username:password)>"
	// We use "agentops" as the username and the token as the password
	if strings.HasPrefix(auth, "Basic ") {
		decoded, err := base64.StdEncoding.DecodeString(auth[6:])
		if err != nil {
			return false
		}
		// Format: username:password - we only care about the password (token)
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 {
			return false
		}
		return parts[1] == p.authToken
	}

	return false
}

func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Create outgoing request
	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	// Copy headers
	for key, values := range r.Header {
		for _, value := range values {
			outReq.Header.Add(key, value)
		}
	}

	// Inject credentials if available
	host := r.URL.Hostname()
	if auth, ok := p.getCredential(host); ok {
		outReq.Header.Set("Authorization", auth)
	}

	// Remove proxy headers
	outReq.Header.Del("Proxy-Connection")
	outReq.Header.Del("Proxy-Authorization")

	// Forward request
	resp, err := http.DefaultTransport.RoundTrip(outReq)
	duration := time.Since(start)

	// Log the request
	if p.logger != nil {
		statusCode := 0
		var errMsg error
		if err != nil {
			errMsg = err
		} else {
			statusCode = resp.StatusCode
		}
		p.logger(r.Method, r.URL.String(), statusCode, duration, errMsg)
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body) // Best-effort copy to response writer
}

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	// Extract host without port for credential lookup
	host, _, _ := net.SplitHostPort(r.Host)
	if host == "" {
		host = r.Host
	}

	// If we have credentials for this host and a CA, do TLS interception
	if p.ca != nil && p.hasCredential(host) {
		p.handleConnectWithInterception(w, r, host)
		return
	}

	// Otherwise, do normal tunneling
	p.handleConnectTunnel(w, r)
}

// handleConnectTunnel creates a transparent TCP tunnel (no interception).
func (p *Proxy) handleConnectTunnel(w http.ResponseWriter, r *http.Request) {
	targetConn, err := net.Dial("tcp", r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		targetConn.Close()
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		targetConn.Close()
		return
	}

	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// Tunnel data bidirectionally with proper cleanup
	var closeOnce sync.Once
	closeConns := func() {
		closeOnce.Do(func() {
			clientConn.Close()
			targetConn.Close()
		})
	}

	go func() {
		_, _ = io.Copy(targetConn, clientConn)
		closeConns()
	}()
	go func() {
		_, _ = io.Copy(clientConn, targetConn)
		closeConns()
	}()
}

// handleConnectWithInterception performs TLS interception to inject credentials.
func (p *Proxy) handleConnectWithInterception(w http.ResponseWriter, r *http.Request, host string) {
	// Hijack the client connection
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// Send 200 OK to client
	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// Generate certificate for this host
	cert, err := p.ca.GenerateCert(host)
	if err != nil {
		return
	}

	// Wrap client connection with TLS (we're the "server" to the client)
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*cert},
		MinVersion:   tls.VersionTLS12,
	}
	tlsClientConn := tls.Server(clientConn, tlsConfig)
	if err := tlsClientConn.Handshake(); err != nil {
		return
	}
	defer tlsClientConn.Close()

	// Create HTTPS client to talk to the real server
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			// Use system CA pool for connecting to real servers
		},
	}

	// Read and forward requests from client
	clientReader := bufio.NewReader(tlsClientConn)
	for {
		req, err := http.ReadRequest(clientReader)
		if err != nil {
			return // Client closed connection or error
		}

		// Set the full URL for the outgoing request
		req.URL.Scheme = "https"
		req.URL.Host = r.Host
		req.RequestURI = ""

		// Inject credentials
		if auth, ok := p.getCredential(host); ok {
			req.Header.Set("Authorization", auth)
		}

		// Remove proxy headers
		req.Header.Del("Proxy-Connection")
		req.Header.Del("Proxy-Authorization")

		// Forward to real server
		start := time.Now()
		resp, err := transport.RoundTrip(req)
		duration := time.Since(start)

		// Log the request
		if p.logger != nil {
			statusCode := 0
			var errMsg error
			if err != nil {
				errMsg = err
			} else {
				statusCode = resp.StatusCode
			}
			p.logger(req.Method, req.URL.String(), statusCode, duration, errMsg)
		}

		if err != nil {
			// Send error response to client
			errResp := &http.Response{
				StatusCode: http.StatusBadGateway,
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     make(http.Header),
			}
			_ = errResp.Write(tlsClientConn)
			continue
		}

		// Send response back to client
		_ = resp.Write(tlsClientConn)
		resp.Body.Close()

		// Check if connection should be closed
		if resp.Close || req.Close {
			return
		}
	}
}
