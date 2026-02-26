// Package proxy provides a TLS-intercepting HTTP proxy for credential injection.
//
// # Security Model
//
// The proxy intercepts HTTPS traffic via CONNECT tunneling with dynamic certificate
// generation. It injects credentials (Authorization headers, etc.) for configured
// hosts without exposing raw tokens to the container.
//
// # Firewall Integration
//
// Container firewall rules (iptables) work in conjunction with the proxy:
//
//   - Docker: Proxy binds to 127.0.0.1 (localhost only). Containers reach it via
//     host.docker.internal or host network mode. Firewall allows proxy port only.
//
//   - Apple containers: Proxy binds to 0.0.0.0 with per-run token authentication.
//     Security is maintained via cryptographic tokens in HTTP_PROXY URL, not IP filtering.
//
// The firewall rules intentionally do NOT filter by destination IP for the proxy port.
// This is because host.docker.internal resolves to different IPs across environments.
// The security boundaries are:
//
//  1. Random high port assignment (reduces collision with other services)
//  2. Token authentication for Apple containers
//  3. Container isolation (other containers can't reach host ports by default)
//
// This trade-off prioritizes reliability over defense-in-depth. The proxy validates
// credentials are only injected for explicitly configured hosts.
package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
)

// contextKey is the type for request-scoped context values.
type contextKey int

const runContextKey contextKey = iota

// MaxBodySize is the maximum size of request/response bodies to capture (8KB).
// Only this much is buffered for logging; the full body is always forwarded.
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
	RunID              string // Run ID from per-run context (daemon mode)
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

// readCloserWrapper wraps a Reader and Closer together.
type readCloserWrapper struct {
	io.Reader
	io.Closer
}

// captureBody reads up to MaxBodySize bytes from a body for logging, returning
// the captured data and a new ReadCloser that streams the full content.
// For small bodies (<=MaxBodySize), the body is fully buffered.
// For large bodies, only MaxBodySize is buffered; the rest streams through.
func captureBody(body io.ReadCloser, contentType string) ([]byte, io.ReadCloser) {
	if body == nil {
		return nil, nil
	}

	// Skip binary content types - don't capture but still pass through
	if !isTextContentType(contentType) {
		return nil, body
	}

	// Read first MaxBodySize bytes for capture/logging
	captureBuf := make([]byte, MaxBodySize)
	n, err := io.ReadFull(body, captureBuf)

	if err == io.EOF || err == io.ErrUnexpectedEOF {
		// Body was <= MaxBodySize, we got it all
		body.Close()
		captured := captureBuf[:n]
		return captured, io.NopCloser(bytes.NewReader(captured))
	}

	if err != nil {
		// Read error - return what we got
		body.Close()
		captured := captureBuf[:n]
		return captured, io.NopCloser(bytes.NewReader(captured))
	}

	// Body is larger than MaxBodySize - stream the rest through
	// Chain captured bytes with remaining body for full forwarding
	captured := captureBuf[:n]
	fullBody := io.MultiReader(bytes.NewReader(captured), body)
	return captured, &readCloserWrapper{Reader: fullBody, Closer: body}
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
// The ctxReq parameter provides the RunContextData (from CONNECT or HTTP request context)
// for extracting the RunID; it may be nil when context is unavailable.
func (p *Proxy) logRequest(ctxReq *http.Request, method, url string, statusCode int, duration time.Duration, err error, reqHeaders, respHeaders http.Header, reqBody, respBody []byte, authInjected bool, injectedHeaderName string) {
	if p.logger == nil {
		return
	}
	var runID string
	if ctxReq != nil {
		if rc := getRunContext(ctxReq); rc != nil {
			runID = rc.RunID
		}
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
		RunID:              runID,
	})
}

// credentialHeader holds a header name and value for credential injection.
type credentialHeader struct {
	Name  string // Header name (e.g., "Authorization", "x-api-key")
	Value string // Header value (e.g., "Bearer token", "sk-ant-...")
	Grant string // Grant name for logging (e.g., "github", "anthropic")
}

// extraHeader holds an additional header to inject for a host.
type extraHeader struct {
	Name  string
	Value string
}

// tokenSubstitution maps a placeholder string to the real token for a host.
type tokenSubstitution struct {
	placeholder string
	realToken   string
}

// CredentialHeader is the exported version of credentialHeader for daemon use.
type CredentialHeader = credentialHeader

// ExtraHeader is the exported version of extraHeader for daemon use.
type ExtraHeader = extraHeader

// TokenSubstitution is the exported version of tokenSubstitution for daemon use.
type TokenSubstitution = tokenSubstitution

// NewTokenSubstitution creates a TokenSubstitution with the given placeholder and real token.
func NewTokenSubstitution(placeholder, realToken string) *TokenSubstitution {
	return &TokenSubstitution{placeholder: placeholder, realToken: realToken}
}

// HostPattern is the exported version of hostPattern.
type HostPattern = hostPattern

// ParseHostPattern is the exported wrapper for parseHostPattern.
func ParseHostPattern(pattern string) HostPattern {
	return parseHostPattern(pattern)
}

// RunContextData holds per-run credential data resolved by ContextResolver.
type RunContextData struct {
	RunID                string
	Credentials          map[string]credentialHeader
	ExtraHeaders         map[string][]extraHeader
	RemoveHeaders        map[string][]string
	TokenSubstitutions   map[string]*tokenSubstitution
	ResponseTransformers map[string][]credential.ResponseTransformer
	MCPServers           []config.MCPServerConfig
	Policy               string
	AllowedHosts         []hostPattern
	AWSHandler           http.Handler
	CredStore            credential.Store
	Relays               map[string]string
}

// ContextResolver resolves a proxy auth token to per-run context data.
type ContextResolver func(token string) (*RunContextData, bool)

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
	credentials          map[string]credentialHeader                 // host -> credential header
	extraHeaders         map[string][]extraHeader                    // host -> additional headers to inject
	responseTransformers map[string][]credential.ResponseTransformer // host -> response transformers
	mu                   sync.RWMutex
	ca                   *CA           // Optional CA for TLS interception
	logger               RequestLogger // Optional request logger
	authToken            string        // Optional auth token required for proxy access
	policy               string        // "permissive" or "strict"
	allowedHosts         []hostPattern // parsed allow patterns for strict policy
	awsHandler           http.Handler  // Optional handler for AWS credential endpoint
	credStore            credential.Store
	mcpServers           []config.MCPServerConfig
	removeHeaders        map[string][]string           // host -> []headerName
	tokenSubstitutions   map[string]*tokenSubstitution // host -> substitution
	relays               map[string]string             // name -> target URL for relay endpoints
	contextResolver      ContextResolver               // optional per-run credential resolver
}

// NewProxy creates a new auth proxy.
func NewProxy() *Proxy {
	return &Proxy{
		credentials:          make(map[string]credentialHeader),
		extraHeaders:         make(map[string][]extraHeader),
		responseTransformers: make(map[string][]credential.ResponseTransformer),
		removeHeaders:        make(map[string][]string),
		tokenSubstitutions:   make(map[string]*tokenSubstitution),
		policy:               "permissive", // default to permissive
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

// SetAWSHandler sets the handler for AWS credential requests.
func (p *Proxy) SetAWSHandler(h http.Handler) {
	p.awsHandler = h
}

// SetMCPServers configures MCP servers for credential injection.
func (p *Proxy) SetMCPServers(servers []config.MCPServerConfig) {
	p.mcpServers = servers
}

// SetCredentialStore sets the credential store for MCP credential retrieval.
func (p *Proxy) SetCredentialStore(store credential.Store) {
	p.credStore = store
}

// SetContextResolver sets the per-run context resolver for multi-tenant proxy use.
// When set, the proxy can resolve auth tokens to per-run credential data.
func (p *Proxy) SetContextResolver(resolver ContextResolver) {
	p.contextResolver = resolver
}

// ResolveContext looks up per-run context data by auth token.
// Returns nil, false when no resolver is set or the token is not found.
func (p *Proxy) ResolveContext(token string) (*RunContextData, bool) {
	if p.contextResolver == nil {
		return nil, false
	}
	return p.contextResolver(token)
}

// SetCredential sets the credential for a host using the Authorization header.
func (p *Proxy) SetCredential(host, authHeader string) {
	p.SetCredentialHeader(host, "Authorization", authHeader)
}

// SetCredentialHeader sets a custom credential header for a host.
// Use this for APIs that use non-standard header names like "x-api-key".
// The host must be a valid hostname (not empty, no path components).
func (p *Proxy) SetCredentialHeader(host, headerName, headerValue string) {
	p.SetCredentialWithGrant(host, headerName, headerValue, "")
}

// SetCredentialWithGrant sets a credential header with grant info for logging.
// Grant is used for structured logging to identify the credential source.
func (p *Proxy) SetCredentialWithGrant(host, headerName, headerValue, grant string) {
	if !isValidHost(host) {
		log.Debug("ignoring invalid host for credential injection",
			"subsystem", "proxy",
			"host", host,
			"header", headerName)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.credentials[host] = credentialHeader{Name: headerName, Value: headerValue, Grant: grant}
}

// AddExtraHeader adds an additional header to inject for a host.
// This is used for headers beyond the main credential header, such as
// beta feature flags or API version headers.
// The host must be a valid hostname (not empty, no path components).
func (p *Proxy) AddExtraHeader(host, headerName, headerValue string) {
	if !isValidHost(host) {
		log.Debug("ignoring invalid host for extra header injection", "host", host, "header", headerName)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.extraHeaders[host] = append(p.extraHeaders[host], extraHeader{Name: headerName, Value: headerValue})
}

// AddResponseTransformer registers a response transformer for a host.
// Transformers are called in registration order after receiving the upstream response.
// Each transformer can inspect and optionally modify the response.
// The host must be a valid hostname (not empty, no path components).
func (p *Proxy) AddResponseTransformer(host string, transformer credential.ResponseTransformer) {
	if !isValidHost(host) {
		log.Debug("ignoring invalid host for response transformer", "host", host)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.responseTransformers[host] = append(p.responseTransformers[host], transformer)
}

// RemoveRequestHeader removes a client-sent header before forwarding.
func (p *Proxy) RemoveRequestHeader(host, headerName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.removeHeaders[host] = append(p.removeHeaders[host], headerName)
}

// SetTokenSubstitution replaces placeholder tokens with real tokens
// in both Authorization headers and request bodies for a specific host.
func (p *Proxy) SetTokenSubstitution(host, placeholder, realToken string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tokenSubstitutions[host] = &tokenSubstitution{
		placeholder: placeholder,
		realToken:   realToken,
	}
}

// getTokenSubstitution returns the token substitution for a host.
func (p *Proxy) getTokenSubstitution(host string) *tokenSubstitution {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if sub, ok := p.tokenSubstitutions[host]; ok {
		return sub
	}
	h, _, _ := net.SplitHostPort(host)
	if h != "" {
		return p.tokenSubstitutions[h]
	}
	return nil
}

// getRemoveHeaders returns header names to remove for a host.
func (p *Proxy) getRemoveHeaders(host string) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if headers, ok := p.removeHeaders[host]; ok {
		return headers
	}
	h, _, _ := net.SplitHostPort(host)
	if h != "" {
		return p.removeHeaders[h]
	}
	return nil
}

// maxTokenSubBodySize is the maximum request body size for token substitution.
// Larger bodies (like file uploads) are not substituted to avoid memory issues.
const maxTokenSubBodySize = 64 * 1024

// applyTokenSubstitution replaces placeholder tokens with real tokens in
// the request's URL path, Authorization header, and body.
// URL path substitution is needed for APIs like Telegram Bot API where
// the token is embedded in the URL (e.g., /bot{TOKEN}/sendMessage).
func (p *Proxy) applyTokenSubstitution(req *http.Request, sub *tokenSubstitution) {
	// Replace in URL path
	if newPath := strings.ReplaceAll(req.URL.Path, sub.placeholder, sub.realToken); newPath != req.URL.Path {
		req.URL.Path = newPath
		if req.URL.RawPath != "" {
			req.URL.RawPath = strings.ReplaceAll(req.URL.RawPath, sub.placeholder, sub.realToken)
		}
	}

	// Replace in Authorization header
	if auth := req.Header.Get("Authorization"); auth != "" {
		if newAuth := strings.ReplaceAll(auth, sub.placeholder, sub.realToken); newAuth != auth {
			req.Header.Set("Authorization", newAuth)
		}
	}

	// Replace in request body (limited to maxTokenSubBodySize)
	if req.Body != nil && req.ContentLength > 0 && req.ContentLength <= maxTokenSubBodySize {
		bodyBytes, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err == nil {
			bodyStr := string(bodyBytes)
			if newBody := strings.ReplaceAll(bodyStr, sub.placeholder, sub.realToken); newBody != bodyStr {
				bodyBytes = []byte(newBody)
			}
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			req.ContentLength = int64(len(bodyBytes))
		}
	}
}

// isValidHost checks if a host string is valid for credential injection.
// Returns false for empty strings, paths, or other invalid values.
func isValidHost(host string) bool {
	if host == "" {
		return false
	}
	// Reject anything that looks like a path or URL
	if strings.ContainsAny(host, "/:@") {
		return false
	}
	// Reject whitespace
	if strings.ContainsAny(host, " \t\n\r") {
		return false
	}
	return true
}

// SetNetworkPolicy sets the network policy and allowed hosts.
// policy should be "permissive" or "strict".
// allows is a list of host patterns like "api.example.com" or "*.example.com".
// grants is a list of grant names like "github" that will be expanded to host patterns.
func (p *Proxy) SetNetworkPolicy(policy string, allows []string, grants []string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.policy = policy
	p.allowedHosts = nil

	// Parse explicit allow patterns
	for _, pattern := range allows {
		p.allowedHosts = append(p.allowedHosts, parseHostPattern(pattern))
	}

	// Add hosts from grants
	for _, grant := range grants {
		grantHosts := GetHostsForGrant(grant)
		for _, pattern := range grantHosts {
			p.allowedHosts = append(p.allowedHosts, parseHostPattern(pattern))
		}
	}
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

// mergeExtraHeaders injects extra headers into a request. If the request
// already has a value for a header, the new value is appended with a comma
// separator (standard HTTP multi-value format). This preserves client-sent
// flags like anthropic-beta while adding proxy-injected flags.
//
// Note: comma-joining is correct for list-valued headers (RFC 9110 §5.3) like
// anthropic-beta, Accept, Cache-Control, etc. It is NOT correct for headers
// like Set-Cookie that cannot be combined. All headers currently registered
// via routing.go are list-safe; if that changes, this function will need a
// per-header strategy.
func mergeExtraHeaders(req *http.Request, host string, headers []extraHeader) {
	for _, h := range headers {
		if existing := req.Header.Get(h.Name); existing != "" {
			req.Header.Set(h.Name, existing+","+h.Value)
		} else {
			req.Header.Set(h.Name, h.Value)
		}
	}
	if len(headers) > 0 {
		log.Debug("extra headers injected",
			"subsystem", "proxy",
			"action", "inject-extra",
			"host", host,
			"count", len(headers))
	}
}

// getExtraHeaders returns additional headers to inject for a host.
func (p *Proxy) getExtraHeaders(host string) []extraHeader {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if headers, ok := p.extraHeaders[host]; ok {
		return headers
	}
	h, _, _ := net.SplitHostPort(host)
	if h != "" {
		return p.extraHeaders[h]
	}
	return nil
}

// getResponseTransformers returns response transformers for a host.
func (p *Proxy) getResponseTransformers(host string) []credential.ResponseTransformer {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if transformers, ok := p.responseTransformers[host]; ok {
		return transformers
	}
	h, _, _ := net.SplitHostPort(host)
	if h != "" {
		return p.responseTransformers[h]
	}
	return nil
}

// getRunContext extracts per-run context data from the request context.
// Returns nil when no RunContextData is present (legacy mode).
func getRunContext(r *http.Request) *RunContextData {
	if rc, ok := r.Context().Value(runContextKey).(*RunContextData); ok {
		return rc
	}
	return nil
}

// getCredentialForRequest returns the credential for a host, checking
// RunContextData first, then falling back to the proxy's own map.
func (p *Proxy) getCredentialForRequest(r *http.Request, host string) (credentialHeader, bool) {
	if rc := getRunContext(r); rc != nil {
		if cred, ok := rc.Credentials[host]; ok {
			return cred, true
		}
		h, _, _ := net.SplitHostPort(host)
		if h != "" {
			if cred, ok := rc.Credentials[h]; ok {
				return cred, true
			}
		}
		return credentialHeader{}, false
	}
	return p.getCredential(host)
}

// getExtraHeadersForRequest returns extra headers for a host, checking
// RunContextData first, then falling back to the proxy's own map.
func (p *Proxy) getExtraHeadersForRequest(r *http.Request, host string) []extraHeader {
	if rc := getRunContext(r); rc != nil {
		if headers, ok := rc.ExtraHeaders[host]; ok {
			return headers
		}
		h, _, _ := net.SplitHostPort(host)
		if h != "" {
			return rc.ExtraHeaders[h]
		}
		return nil
	}
	return p.getExtraHeaders(host)
}

// getRemoveHeadersForRequest returns headers to remove for a host, checking
// RunContextData first, then falling back to the proxy's own map.
func (p *Proxy) getRemoveHeadersForRequest(r *http.Request, host string) []string {
	if rc := getRunContext(r); rc != nil {
		if headers, ok := rc.RemoveHeaders[host]; ok {
			return headers
		}
		h, _, _ := net.SplitHostPort(host)
		if h != "" {
			return rc.RemoveHeaders[h]
		}
		return nil
	}
	return p.getRemoveHeaders(host)
}

// getTokenSubstitutionForRequest returns the token substitution for a host,
// checking RunContextData first, then falling back to the proxy's own map.
func (p *Proxy) getTokenSubstitutionForRequest(r *http.Request, host string) *tokenSubstitution {
	if rc := getRunContext(r); rc != nil {
		if sub, ok := rc.TokenSubstitutions[host]; ok {
			return sub
		}
		h, _, _ := net.SplitHostPort(host)
		if h != "" {
			return rc.TokenSubstitutions[h]
		}
		return nil
	}
	return p.getTokenSubstitution(host)
}

// getResponseTransformersForRequest returns response transformers for a host,
// checking RunContextData first, then falling back to the proxy's own map.
func (p *Proxy) getResponseTransformersForRequest(r *http.Request, host string) []credential.ResponseTransformer {
	if rc := getRunContext(r); rc != nil {
		if transformers, ok := rc.ResponseTransformers[host]; ok {
			return transformers
		}
		h, _, _ := net.SplitHostPort(host)
		if h != "" {
			return rc.ResponseTransformers[h]
		}
		return nil
	}
	return p.getResponseTransformers(host)
}

// checkNetworkPolicyForRequest checks network policy using RunContextData first,
// then falling back to the proxy's own policy.
func (p *Proxy) checkNetworkPolicyForRequest(r *http.Request, host string, port int) bool {
	if rc := getRunContext(r); rc != nil {
		if rc.Policy != "strict" {
			return true
		}
		return matchHost(rc.AllowedHosts, host, port)
	}
	return p.checkNetworkPolicy(host, port)
}

// getMCPServersForRequest returns MCP servers from RunContextData or falls
// back to the proxy's own list.
func (p *Proxy) getMCPServersForRequest(r *http.Request) []config.MCPServerConfig {
	if rc := getRunContext(r); rc != nil {
		return rc.MCPServers
	}
	return p.mcpServers
}

// getCredStoreForRequest returns the credential store from RunContextData
// or falls back to the proxy's own store.
func (p *Proxy) getCredStoreForRequest(r *http.Request) credential.Store {
	if rc := getRunContext(r); rc != nil && rc.CredStore != nil {
		return rc.CredStore
	}
	return p.credStore
}

// getAWSHandlerForRequest returns the AWS handler from RunContextData
// or falls back to the proxy's own handler.
func (p *Proxy) getAWSHandlerForRequest(r *http.Request) http.Handler {
	if rc := getRunContext(r); rc != nil && rc.AWSHandler != nil {
		return rc.AWSHandler
	}
	return p.awsHandler
}

// ServeHTTP handles proxy requests.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Relay endpoints are accessed directly (via NO_PROXY bypass), not through
	// the proxy mechanism, so they appear as direct HTTP requests (r.URL.Host is
	// empty). We check r.URL.Host == "" to distinguish direct requests from
	// proxied requests that happen to have /relay/ in the path — without this,
	// a proxied request to http://anything.com/relay/foo would match and bypass auth.
	// Auth is skipped because direct requests don't carry Proxy-Authorization.
	// Safety: relays only forward to pre-configured URLs, not arbitrary hosts.
	if len(p.relays) > 0 && r.URL.Host == "" && strings.HasPrefix(r.URL.Path, "/relay/") {
		p.handleRelay(w, r)
		return
	}

	// Authentication and context resolution.
	// When a contextResolver is set (daemon mode), extract the proxy auth token,
	// resolve it to per-run context data, and store it in the request context.
	// When no contextResolver is set (legacy single-run mode), use p.authToken check.
	if p.contextResolver != nil {
		token, ok := extractProxyToken(r)
		if !ok {
			http.Error(w, "Proxy authentication required", http.StatusProxyAuthRequired)
			return
		}
		rc, found := p.contextResolver(token)
		if !found {
			http.Error(w, "Invalid proxy token", http.StatusProxyAuthRequired)
			return
		}
		ctx := context.WithValue(r.Context(), runContextKey, rc)
		r = r.WithContext(ctx)
	} else if p.authToken != "" && !p.checkAuth(r) {
		http.Error(w, "Proxy authentication required", http.StatusProxyAuthRequired)
		return
	}

	// Handle AWS credential endpoint
	if awsH := p.getAWSHandlerForRequest(r); awsH != nil && strings.HasPrefix(r.URL.Path, "/_aws/credentials") {
		awsH.ServeHTTP(w, r)
		return
	}

	// Handle MCP relay endpoint
	if strings.HasPrefix(r.URL.Path, "/mcp/") {
		p.handleMCPRelay(w, r)
		return
	}

	// Inject MCP credentials if request matches configured server
	p.injectMCPCredentials(r)

	// Log the proxied request
	if r.Method == http.MethodConnect {
		host, port, _ := net.SplitHostPort(r.Host)
		log.Debug("proxy connect",
			"subsystem", "proxy",
			"action", "connect",
			"host", host,
			"port", port)
		p.handleConnect(w, r)
		return
	}

	log.Debug("proxy request",
		"subsystem", "proxy",
		"action", "forward",
		"method", r.Method,
		"host", r.URL.Hostname(),
		"port", r.URL.Port(),
		"path", r.URL.Path)
	p.handleHTTP(w, r)
}

// extractProxyToken extracts the token from a Proxy-Authorization header.
// Supports both Basic auth (from HTTP_PROXY=http://moat:token@host) and Bearer format.
// Returns the extracted token and true, or empty string and false if no valid token found.
func extractProxyToken(r *http.Request) (string, bool) {
	auth := r.Header.Get("Proxy-Authorization")
	if auth == "" {
		return "", false
	}

	if strings.HasPrefix(auth, "Bearer ") {
		return auth[7:], true
	}

	if strings.HasPrefix(auth, "Basic ") {
		decoded, err := base64.StdEncoding.DecodeString(auth[6:])
		if err != nil {
			return "", false
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 {
			return "", false
		}
		return parts[1], true
	}

	return "", false
}

// checkAuth validates the Proxy-Authorization header against the required token.
// Accepts both Basic auth (from HTTP_PROXY=http://moat:token@host) and Bearer format.
// Uses constant-time comparison to prevent timing attacks.
func (p *Proxy) checkAuth(r *http.Request) bool {
	token, ok := extractProxyToken(r)
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(p.authToken)) == 1
}

// checkNetworkPolicy checks if the host:port is allowed by the network policy.
// Returns true if allowed, false if blocked.
func (p *Proxy) checkNetworkPolicy(host string, port int) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Permissive policy allows everything
	if p.policy != "strict" {
		return true
	}

	// Strict policy requires host to match allowedHosts
	return matchHost(p.allowedHosts, host, port)
}

// writeBlockedResponse writes a 407 response when a request is blocked by network policy.
func (p *Proxy) writeBlockedResponse(w http.ResponseWriter, host string) {
	w.Header().Set("X-Moat-Blocked", "network-policy")
	w.Header().Set("Proxy-Authenticate", "Moat-Policy")
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusProxyAuthRequired)
	_, _ = w.Write([]byte("Moat: request blocked by network policy.\nHost \"" + host + "\" is not in the allow list.\nAdd it to network.allow in agent.yaml or use policy: permissive.\n"))
}

func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Extract host and infer port from scheme
	host := r.URL.Hostname()
	cred, authInjected := p.getCredentialForRequest(r, host)

	// Capture request body and headers before forwarding
	var reqBody []byte
	reqBody, r.Body = captureBody(r.Body, r.Header.Get("Content-Type"))
	originalReqHeaders := r.Header.Clone()

	port := 80
	if r.URL.Scheme == "https" {
		port = 443
	}
	if r.URL.Port() != "" {
		// Port explicitly specified in URL
		var err error
		port, err = net.LookupPort("tcp", r.URL.Port())
		if err != nil {
			port = 80 // fallback
		}
	}

	// Check network policy
	if !p.checkNetworkPolicyForRequest(r, host, port) {
		duration := time.Since(start)
		// Log blocked request
		p.logRequest(r, r.Method, r.URL.String(), http.StatusProxyAuthRequired, duration, nil, originalReqHeaders, nil, reqBody, nil, false, "")
		// Send 407 response with policy headers
		p.writeBlockedResponse(w, host)
		return
	}

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
		log.Debug("credential injected",
			"subsystem", "proxy",
			"action", "inject",
			"grant", cred.Grant,
			"host", host,
			"header", cred.Name,
			"method", r.Method,
			"path", r.URL.Path)
	}
	// Inject any additional headers configured for this host.
	// Merges with existing values (comma-separated) to preserve client
	// headers like anthropic-beta that support multiple flags.
	mergeExtraHeaders(outReq, host, p.getExtraHeadersForRequest(r, host))

	outReq.Header.Del("Proxy-Connection")
	outReq.Header.Del("Proxy-Authorization")

	// Remove headers that should be stripped
	for _, headerName := range p.getRemoveHeadersForRequest(r, host) {
		outReq.Header.Del(headerName)
	}
	// Apply token substitution if configured.
	// Substitution targets outReq (not r), so r.URL.String() used for logging
	// below still contains the placeholder, not the real token.
	if sub := p.getTokenSubstitutionForRequest(r, host); sub != nil {
		p.applyTokenSubstitution(outReq, sub)
	}

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

	logCredHeaderName := ""
	if authInjected {
		logCredHeaderName = cred.Name
	}
	p.logRequest(r, r.Method, r.URL.String(), statusCode, duration, err, originalReqHeaders, respHeaders, reqBody, respBody, authInjected, logCredHeaderName)

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
	// Extract host and port for network policy check
	host, portStr, err := net.SplitHostPort(r.Host)
	if err != nil {
		// r.Host should always have port in CONNECT requests
		http.Error(w, "invalid host format", http.StatusBadRequest)
		return
	}

	port, err := net.LookupPort("tcp", portStr)
	if err != nil {
		http.Error(w, "invalid port", http.StatusBadRequest)
		return
	}

	// Check network policy before establishing tunnel
	if !p.checkNetworkPolicyForRequest(r, host, port) {
		// Log blocked request
		if p.logger != nil {
			p.logRequest(r, r.Method, r.Host, http.StatusProxyAuthRequired, 0, nil, nil, nil, nil, nil, false, "")
		}
		// Send 407 response with policy headers
		p.writeBlockedResponse(w, host)
		return
	}

	// Do MITM interception when we have a CA configured.
	//
	// Security note: This intercepts ALL HTTPS traffic, not just credential-injected hosts.
	// This is intentional for full observability - a core AgentOps feature. The container
	// trusts our CA (mounted at /etc/ssl/certs/agentops-ca/) and we verify upstream certs.
	//
	// Applications with certificate pinning may fail. This is expected behavior since
	// observability requires seeing all traffic.
	if p.ca != nil {
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
	cred, authInjected := p.getCredentialForRequest(r, host)

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
		MaxIdleConns:    100,
		IdleConnTimeout: 90 * time.Second,
		// Note: Do NOT set ForceAttemptHTTP2 here. This transport forwards
		// HTTP/1.1 requests read from the intercepted TLS connection. Enabling
		// HTTP/2 on the upstream side causes framing mismatches and hangs.
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

		// Inject MCP credentials if this is an MCP request.
		// Use the CONNECT request r for context lookups since inner
		// requests from the TLS stream don't carry the request context.
		p.injectMCPCredentialsWithContext(r, req)

		if authInjected {
			req.Header.Set(cred.Name, cred.Value)
			log.Debug("credential injected",
				"subsystem", "proxy",
				"action", "inject",
				"grant", cred.Grant,
				"host", host,
				"header", cred.Name,
				"method", req.Method,
				"path", req.URL.Path)
		}
		// Inject any additional headers configured for this host.
		// Merges with existing values (comma-separated) to preserve client
		// headers like anthropic-beta that support multiple flags.
		mergeExtraHeaders(req, r.Host, p.getExtraHeadersForRequest(r, r.Host))
		req.Header.Del("Proxy-Connection")
		req.Header.Del("Proxy-Authorization")

		// Remove headers that should be stripped for this host
		for _, headerName := range p.getRemoveHeadersForRequest(r, host) {
			req.Header.Del(headerName)
		}
		// Apply token substitution if configured for this host.
		// Capture the URL before substitution so logs don't contain real tokens.
		logURL := req.URL.String()
		if sub := p.getTokenSubstitutionForRequest(r, host); sub != nil {
			p.applyTokenSubstitution(req, sub)
		}

		start := time.Now()
		resp, err := transport.RoundTrip(req)
		duration := time.Since(start)

		// Capture response
		var respBody []byte
		var respHeaders http.Header
		var statusCode int
		if resp != nil {
			respHeaders = resp.Header.Clone()
			statusCode = resp.StatusCode

			// Apply response transformers BEFORE capturing body
			// so transformer can read the original response body.
			// Only the first transformer that returns true is applied (transformers are not chained).
			if transformers := p.getResponseTransformersForRequest(r, host); len(transformers) > 0 {
				for _, transformer := range transformers {
					if newRespInterface, transformed := transformer(req, resp); transformed {
						if newResp, ok := newRespInterface.(*http.Response); ok {
							resp = newResp
							statusCode = resp.StatusCode
							respHeaders = resp.Header.Clone()
						}
						break // Only apply first matching transformer
					}
				}
			}

			// Capture body AFTER transformation
			respBody, resp.Body = captureBody(resp.Body, resp.Header.Get("Content-Type"))
		}

		logCredHeaderName := ""
		if authInjected {
			logCredHeaderName = cred.Name
		}
		p.logRequest(r, req.Method, logURL, statusCode, duration, err, originalReqHeaders, respHeaders, reqBody, respBody, authInjected, logCredHeaderName)

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
