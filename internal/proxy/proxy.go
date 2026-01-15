package proxy

import (
	"bufio"
	"bytes"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// MaxBodySize is the maximum size of request/response bodies to capture (8KB).
const MaxBodySize = 8 * 1024

// RequestLogData contains all data for a logged request.
type RequestLogData struct {
	Method             string
	URL                string
	StatusCode         int
	Duration           time.Duration
	Err                error
	RequestHeaders     http.Header
	ResponseHeaders    http.Header
	RequestBody        []byte
	ResponseBody       []byte
	AuthInjected       bool   // True if credential header was injected for this host
	InjectedHeaderName string // Name of the injected header (for filtering)
}

// RequestLogger is called for each proxied request.
type RequestLogger func(data RequestLogData)

// isTextContentType returns true for text-based content types that should be captured.
func isTextContentType(ct string) bool {
	if ct == "" {
		return false
	}
	ct = strings.ToLower(ct)
	return strings.HasPrefix(ct, "text/") ||
		strings.Contains(ct, "json") ||
		strings.Contains(ct, "xml") ||
		strings.Contains(ct, "x-www-form-urlencoded") ||
		strings.Contains(ct, "javascript")
}

// captureBody reads up to MaxBodySize bytes from a body, returning the captured
// data and a new ReadCloser that replays the full content for forwarding.
func captureBody(body io.ReadCloser, contentType string) ([]byte, io.ReadCloser) {
	if body == nil {
		return nil, nil
	}

	// Skip binary content types
	if !isTextContentType(contentType) {
		return nil, body
	}

	// Read up to MaxBodySize + 1 to detect truncation
	data, err := io.ReadAll(io.LimitReader(body, MaxBodySize+1))
	if err != nil {
		return nil, body
	}

	// Read any remaining data (we still need to forward it)
	remaining, _ := io.ReadAll(body)
	body.Close()

	// Truncate captured data if needed
	captured := data
	if len(captured) > MaxBodySize {
		captured = captured[:MaxBodySize]
	}

	// Create new reader that replays all data for forwarding
	fullData := append(data, remaining...)
	return captured, io.NopCloser(bytes.NewReader(fullData))
}

// FilterHeaders creates a copy of headers with sensitive values filtered.
// If authInjected is true, the specified header name is redacted.
func FilterHeaders(headers http.Header, authInjected bool, injectedHeaderName string) map[string]string {
	if headers == nil {
		return nil
	}

	result := make(map[string]string)
	for key, values := range headers {
		// Always filter proxy headers
		if strings.EqualFold(key, "Proxy-Authorization") || strings.EqualFold(key, "Proxy-Connection") {
			continue
		}
		// Redact the injected credential header
		if authInjected && strings.EqualFold(key, injectedHeaderName) {
			result[key] = "[REDACTED]"
			continue
		}
		// Join multiple values with comma (standard HTTP practice)
		result[key] = strings.Join(values, ", ")
	}
	return result
}

// logRequest is a helper that logs request data if a logger is configured.
func (p *Proxy) logRequest(method, url string, statusCode int, duration time.Duration, err error, reqHeaders, respHeaders http.Header, reqBody, respBody []byte, authInjected bool, injectedHeaderName string) {
	if p.logger == nil {
		return
	}
	p.logger(RequestLogData{
		Method:             method,
		URL:                url,
		StatusCode:         statusCode,
		Duration:           duration,
		Err:                err,
		RequestHeaders:     reqHeaders,
		ResponseHeaders:    respHeaders,
		RequestBody:        reqBody,
		ResponseBody:       respBody,
		AuthInjected:       authInjected,
		InjectedHeaderName: injectedHeaderName,
	})
}

// credentialHeader holds a header name and value for credential injection.
type credentialHeader struct {
	Name  string // Header name (e.g., "Authorization", "x-api-key")
	Value string // Header value (e.g., "Bearer token", "sk-ant-...")
}

// Proxy is an HTTP proxy that injects credentials into outgoing requests.
//
// # Security Model
//
// The proxy handles two distinct security concerns:
//
//  1. Credential injection: The proxy injects credential headers for
//     configured hosts (e.g., api.github.com, api.anthropic.com). When CA
//     is set, it performs TLS interception (MITM) to inject headers into
//     HTTPS requests. Supports custom header names (Authorization, x-api-key, etc).
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
	credentials map[string]credentialHeader // host -> credential header
	mu          sync.RWMutex
	ca          *CA           // Optional CA for TLS interception
	logger      RequestLogger // Optional request logger
	authToken   string        // Optional auth token required for proxy access
}

// NewProxy creates a new auth proxy.
func NewProxy() *Proxy {
	return &Proxy{
		credentials: make(map[string]credentialHeader),
	}
}

// SetAuthToken sets the required authentication token for proxy access.
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

// SetCredential sets the credential for a host using the Authorization header.
func (p *Proxy) SetCredential(host, authHeader string) {
	p.SetCredentialHeader(host, "Authorization", authHeader)
}

// SetCredentialHeader sets a custom credential header for a host.
// Use this for APIs that use non-standard header names like "x-api-key".
func (p *Proxy) SetCredentialHeader(host, headerName, headerValue string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.credentials[host] = credentialHeader{Name: headerName, Value: headerValue}
}

// getCredential returns the credential header for a host.
func (p *Proxy) getCredential(host string) (credentialHeader, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if cred, ok := p.credentials[host]; ok {
		return cred, true
	}
	h, _, _ := net.SplitHostPort(host)
	if h != "" {
		cred, ok := p.credentials[h]
		return cred, ok
	}
	return credentialHeader{}, false
}

// ServeHTTP handles proxy requests.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.authToken != "" && !p.checkAuth(r) {
		http.Error(w, "Proxy authentication required", http.StatusProxyAuthRequired)
		return
	}

	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleHTTP(w, r)
}

// checkAuth validates the Proxy-Authorization header against the required token.
// Accepts both Basic auth (from HTTP_PROXY=http://agentops:token@host) and Bearer format.
// Uses constant-time comparison to prevent timing attacks.
func (p *Proxy) checkAuth(r *http.Request) bool {
	auth := r.Header.Get("Proxy-Authorization")
	if auth == "" {
		return false
	}

	if strings.HasPrefix(auth, "Bearer ") {
		return subtle.ConstantTimeCompare([]byte(auth[7:]), []byte(p.authToken)) == 1
	}

	if strings.HasPrefix(auth, "Basic ") {
		decoded, err := base64.StdEncoding.DecodeString(auth[6:])
		if err != nil {
			return false
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 {
			return false
		}
		return subtle.ConstantTimeCompare([]byte(parts[1]), []byte(p.authToken)) == 1
	}

	return false
}

func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	host := r.URL.Hostname()
	cred, authInjected := p.getCredential(host)

	// Capture request body and headers before forwarding
	var reqBody []byte
	reqBody, r.Body = captureBody(r.Body, r.Header.Get("Content-Type"))
	originalReqHeaders := r.Header.Clone()

	// Create outgoing request
	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	// Copy headers and inject credentials
	for key, values := range r.Header {
		for _, value := range values {
			outReq.Header.Add(key, value)
		}
	}
	if authInjected {
		outReq.Header.Set(cred.Name, cred.Value)
	}
	outReq.Header.Del("Proxy-Connection")
	outReq.Header.Del("Proxy-Authorization")

	// Forward request
	resp, err := http.DefaultTransport.RoundTrip(outReq)
	duration := time.Since(start)

	// Capture response body and headers
	var respBody []byte
	var respHeaders http.Header
	var statusCode int
	if resp != nil {
		respHeaders = resp.Header.Clone()
		respBody, resp.Body = captureBody(resp.Body, resp.Header.Get("Content-Type"))
		statusCode = resp.StatusCode
	}

	p.logRequest(r.Method, r.URL.String(), statusCode, duration, err, originalReqHeaders, respHeaders, reqBody, respBody, authInjected, cred.Name)

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.Host)
	if host == "" {
		host = r.Host
	}

	if _, hasCredential := p.getCredential(host); p.ca != nil && hasCredential {
		p.handleConnectWithInterception(w, r, host)
		return
	}
	p.handleConnectTunnel(w, r)
}

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

func (p *Proxy) handleConnectWithInterception(w http.ResponseWriter, r *http.Request, host string) {
	cred, authInjected := p.getCredential(host)

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

	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	cert, err := p.ca.GenerateCert(host)
	if err != nil {
		return
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*cert},
		MinVersion:   tls.VersionTLS12,
	}
	tlsClientConn := tls.Server(clientConn, tlsConfig)
	if err := tlsClientConn.Handshake(); err != nil {
		return
	}
	defer tlsClientConn.Close()

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}

	clientReader := bufio.NewReader(tlsClientConn)
	for {
		req, err := http.ReadRequest(clientReader)
		if err != nil {
			return
		}

		// Capture request body and headers
		var reqBody []byte
		reqBody, req.Body = captureBody(req.Body, req.Header.Get("Content-Type"))
		originalReqHeaders := req.Header.Clone()

		req.URL.Scheme = "https"
		req.URL.Host = r.Host
		req.RequestURI = ""

		if authInjected {
			req.Header.Set(cred.Name, cred.Value)
		}
		req.Header.Del("Proxy-Connection")
		req.Header.Del("Proxy-Authorization")

		start := time.Now()
		resp, err := transport.RoundTrip(req)
		duration := time.Since(start)

		// Capture response
		var respBody []byte
		var respHeaders http.Header
		var statusCode int
		if resp != nil {
			respHeaders = resp.Header.Clone()
			respBody, resp.Body = captureBody(resp.Body, resp.Header.Get("Content-Type"))
			statusCode = resp.StatusCode
		}

		p.logRequest(req.Method, req.URL.String(), statusCode, duration, err, originalReqHeaders, respHeaders, reqBody, respBody, authInjected, cred.Name)

		if err != nil {
			errResp := &http.Response{
				StatusCode: http.StatusBadGateway,
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     make(http.Header),
			}
			_ = errResp.Write(tlsClientConn)
			continue
		}

		_ = resp.Write(tlsClientConn)
		resp.Body.Close()

		if resp.Close || req.Close {
			return
		}
	}
}
