package proxy

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// RequestLogger is called for each proxied request.
type RequestLogger func(method, url string, statusCode int, duration time.Duration, err error)

// Proxy is an HTTP proxy that injects credentials.
// When CA is set, it performs TLS interception for HTTPS requests
// to hosts with credentials, allowing header injection.
type Proxy struct {
	credentials map[string]string // host -> auth header value
	mu          sync.RWMutex
	ca          *CA           // Optional CA for TLS interception
	logger      RequestLogger // Optional request logger
}

// NewProxy creates a new auth proxy.
func NewProxy() *Proxy {
	return &Proxy{
		credentials: make(map[string]string),
	}
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
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleHTTP(w, r)
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
	io.Copy(w, resp.Body)
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

	clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// Tunnel data bidirectionally with proper cleanup
	var closeOnce sync.Once
	closeConns := func() {
		closeOnce.Do(func() {
			clientConn.Close()
			targetConn.Close()
		})
	}

	go func() {
		io.Copy(targetConn, clientConn)
		closeConns()
	}()
	go func() {
		io.Copy(clientConn, targetConn)
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
	clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// Generate certificate for this host
	cert, err := p.ca.GenerateCert(host)
	if err != nil {
		return
	}

	// Wrap client connection with TLS (we're the "server" to the client)
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*cert},
	}
	tlsClientConn := tls.Server(clientConn, tlsConfig)
	if err := tlsClientConn.Handshake(); err != nil {
		return
	}
	defer tlsClientConn.Close()

	// Create HTTPS client to talk to the real server
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
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
			errResp.Write(tlsClientConn)
			continue
		}

		// Send response back to client
		resp.Write(tlsClientConn)
		resp.Body.Close()

		// Check if connection should be closed
		if resp.Close || req.Close {
			return
		}
	}
}
