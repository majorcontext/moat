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
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	keeplib "github.com/majorcontext/keep"

	"github.com/majorcontext/moat/internal/hostnames"
)

// contextKey is the type for request-scoped context values.
type contextKey int

const runContextKey contextKey = iota

// ResponseTransformer transforms HTTP responses before body capture.
// Cast to *http.Request and *http.Response in the transformer implementation.
// Returns the modified response and true if transformed, or original and false.
type ResponseTransformer func(req, resp any) (any, bool)

// CredentialStore retrieves tokens by provider name (grant).
// The proxy uses this for MCP credential injection when credentials are
// not pre-resolved in RunContextData.
type CredentialStore interface {
	GetToken(provider string) (string, error)
}

// MCPServerConfig holds the MCP server configuration needed by the proxy.
type MCPServerConfig struct {
	Name string
	URL  string
	Auth *MCPAuthConfig
}

// MCPAuthConfig defines authentication for an MCP server.
type MCPAuthConfig struct {
	Grant  string
	Header string
}

// RequestChecker checks if a request to host:port with the given method and
// path is allowed. Provided by the caller to encapsulate network rule evaluation.
type RequestChecker func(host string, port int, method, path string) bool

// PathRulesChecker reports whether path-level rules exist for a given host.
// When true, the proxy intercepts CONNECT tunnels for path-level inspection.
type PathRulesChecker func(host string, port int) bool

// MaxBodySize is the maximum size of request/response bodies to capture (8KB).
// Only this much is buffered for logging; the full body is always forwarded.
const MaxBodySize = 8 * 1024

// RequestLogData contains all data for a logged request.
type RequestLogData struct {
	Method          string
	URL             string
	StatusCode      int
	Duration        time.Duration
	Err             error
	RequestHeaders  http.Header
	ResponseHeaders http.Header
	RequestBody     []byte
	ResponseBody    []byte
	AuthInjected    bool            // True if any credential header was injected for this host
	InjectedHeaders map[string]bool // Lower-cased header names that were injected
	RunID           string          // Run ID from per-run context (daemon mode)
}

// RequestLogger is called for each proxied request.
type RequestLogger func(data RequestLogData)

// PolicyLogData contains data for a policy denial event.
type PolicyLogData struct {
	RunID     string
	Scope     string
	Operation string
	Rule      string
	Message   string
}

// PolicyLogger is called when a policy denial occurs.
type PolicyLogger func(data PolicyLogData)

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
// injectedHeaders is a set of lower-cased header names whose values should be
// redacted (credential headers the proxy injected).
func FilterHeaders(headers http.Header, injectedHeaders map[string]bool) map[string]string {
	if headers == nil {
		return nil
	}

	result := make(map[string]string)
	for key, values := range headers {
		// Always filter proxy headers
		if strings.EqualFold(key, "Proxy-Authorization") || strings.EqualFold(key, "Proxy-Connection") {
			continue
		}
		// Redact injected credential headers
		if injectedHeaders[strings.ToLower(key)] {
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
func (p *Proxy) logRequest(ctxReq *http.Request, method, url string, statusCode int, duration time.Duration, err error, reqHeaders, respHeaders http.Header, reqBody, respBody []byte, injectedHeaders map[string]bool) {
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
		Method:          method,
		URL:             url,
		StatusCode:      statusCode,
		Duration:        duration,
		Err:             err,
		RequestHeaders:  reqHeaders,
		ResponseHeaders: respHeaders,
		RequestBody:     reqBody,
		ResponseBody:    respBody,
		AuthInjected:    len(injectedHeaders) > 0,
		InjectedHeaders: injectedHeaders,
		RunID:           runID,
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

// MatchesHostPattern reports whether a host:port matches a parsed host pattern.
func MatchesHostPattern(pattern HostPattern, host string, port int) bool {
	return matchesPattern(pattern, host, port)
}

// RunContextData holds per-run credential data resolved by ContextResolver.
type RunContextData struct {
	RunID                string
	Credentials          map[string][]credentialHeader
	ExtraHeaders         map[string][]extraHeader
	RemoveHeaders        map[string][]string
	TokenSubstitutions   map[string]*tokenSubstitution
	ResponseTransformers map[string][]ResponseTransformer
	MCPServers           []MCPServerConfig
	Policy               string
	AllowedHosts         []hostPattern
	RequestCheck         RequestChecker
	PathRulesCheck       PathRulesChecker
	AWSHandler           http.Handler
	CredStore            CredentialStore
	KeepEngines          map[string]*keeplib.Engine
	HostGateway          string
	HostGatewayIP        string // actual IP to forward allowed host-gateway requests to
	AllowedHostPorts     []int
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
	credentials          map[string][]credentialHeader    // host -> credential headers
	extraHeaders         map[string][]extraHeader         // host -> additional headers to inject
	responseTransformers map[string][]ResponseTransformer // host -> response transformers
	mu                   sync.RWMutex
	ca                   *CA              // Optional CA for TLS interception
	logger               RequestLogger    // Optional request logger
	authToken            string           // Optional auth token required for proxy access
	policy               string           // "permissive" or "strict"
	allowedHosts         []hostPattern    // parsed allow patterns for strict policy
	requestChecker       RequestChecker   // per-host request rules checker
	pathRulesChecker     PathRulesChecker // checks if host has path-level rules
	awsHandler           http.Handler     // Optional handler for AWS credential endpoint
	credStore            CredentialStore
	mcpServers           []MCPServerConfig
	removeHeaders        map[string][]string           // host -> []headerName
	tokenSubstitutions   map[string]*tokenSubstitution // host -> substitution
	relays               map[string]string             // name -> target URL for relay endpoints
	contextResolver      ContextResolver               // optional per-run credential resolver
	policyLogger         PolicyLogger                  // optional policy decision logger
	upstreamCAs          *x509.CertPool                // optional CA pool for upstream TLS verification
}

// NewProxy creates a new auth proxy.
func NewProxy() *Proxy {
	return &Proxy{
		credentials:          make(map[string][]credentialHeader),
		extraHeaders:         make(map[string][]extraHeader),
		responseTransformers: make(map[string][]ResponseTransformer),
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

// SetUpstreamCAs sets a custom CA pool for verifying upstream (origin server)
// TLS certificates during CONNECT interception. When nil (the default), the
// system root certificates are used. This is useful for environments with
// private PKI or for testing.
func (p *Proxy) SetUpstreamCAs(pool *x509.CertPool) {
	p.upstreamCAs = pool
}

// SetLogger sets the request logger.
func (p *Proxy) SetLogger(logger RequestLogger) {
	p.logger = logger
}

// SetPolicyLogger sets the policy decision logger.
func (p *Proxy) SetPolicyLogger(logger PolicyLogger) {
	p.policyLogger = logger
}

// logPolicy logs a policy denial if a logger is configured.
func (p *Proxy) logPolicy(ctxReq *http.Request, scope, operation, rule, message string) {
	if p.policyLogger == nil {
		return
	}
	var runID string
	if ctxReq != nil {
		if rc := getRunContext(ctxReq); rc != nil {
			runID = rc.RunID
		}
	}
	p.policyLogger(PolicyLogData{
		RunID:     runID,
		Scope:     scope,
		Operation: operation,
		Rule:      rule,
		Message:   message,
	})
}

// SetAWSHandler sets the handler for AWS credential requests.
func (p *Proxy) SetAWSHandler(h http.Handler) {
	p.awsHandler = h
}

// SetMCPServers configures MCP servers for credential injection.
func (p *Proxy) SetMCPServers(servers []MCPServerConfig) {
	p.mcpServers = servers
}

// SetCredentialStore sets the credential store for MCP credential retrieval.
func (p *Proxy) SetCredentialStore(store CredentialStore) {
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
// If a credential with the same grant and header name already exists for
// the host, it is updated in place (upsert). Otherwise, a new entry is
// appended. Matching on both grant and header name prevents empty-grant
// collisions when SetCredentialHeader is called multiple times with
// different headers.
func (p *Proxy) SetCredentialWithGrant(host, headerName, headerValue, grant string) {
	if !isValidHost(host) {
		slog.Debug("ignoring invalid host for credential injection",
			"subsystem", "proxy",
			"host", host,
			"header", headerName)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := credentialHeader{Name: headerName, Value: headerValue, Grant: grant}
	for i, existing := range p.credentials[host] {
		if existing.Grant == grant && existing.Name == headerName {
			p.credentials[host][i] = entry
			return
		}
	}
	p.credentials[host] = append(p.credentials[host], entry)
}

// AddExtraHeader adds an additional header to inject for a host.
// This is used for headers beyond the main credential header, such as
// beta feature flags or API version headers.
// The host must be a valid hostname (not empty, no path components).
func (p *Proxy) AddExtraHeader(host, headerName, headerValue string) {
	if !isValidHost(host) {
		slog.Debug("ignoring invalid host for extra header injection", "host", host, "header", headerName)
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
func (p *Proxy) AddResponseTransformer(host string, transformer ResponseTransformer) {
	if !isValidHost(host) {
		slog.Debug("ignoring invalid host for response transformer", "host", host)
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
	p.requestChecker = nil
	p.pathRulesChecker = nil

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

// SetNetworkPolicyWithRules sets the network policy with per-host request rules.
// The allows list should include hosts from rules (the caller extracts them).
// checker evaluates per-request rules; pathChecker reports if path-level rules exist.
func (p *Proxy) SetNetworkPolicyWithRules(policy string, allows []string, grants []string, checker RequestChecker, pathChecker PathRulesChecker) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.policy = policy
	p.allowedHosts = nil
	p.requestChecker = checker
	p.pathRulesChecker = pathChecker

	for _, pattern := range allows {
		p.allowedHosts = append(p.allowedHosts, parseHostPattern(pattern))
	}
	for _, grant := range grants {
		for _, pattern := range GetHostsForGrant(grant) {
			p.allowedHosts = append(p.allowedHosts, parseHostPattern(pattern))
		}
	}
}

// getCredentials returns all credential headers for a host.
// Returns a copy of the slice to avoid data races with concurrent
// SetCredentialWithGrant calls (e.g., token refresh).
func (p *Proxy) getCredentials(host string) []credentialHeader {
	p.mu.RLock()
	defer p.mu.RUnlock()
	creds := p.credentials[host]
	if len(creds) == 0 {
		h, _, _ := net.SplitHostPort(host)
		if h != "" {
			creds = p.credentials[h]
		}
	}
	if len(creds) == 0 {
		return nil
	}
	out := make([]credentialHeader, len(creds))
	copy(out, creds)
	return out
}

// injectCredentials replaces credential headers in the request. For each
// credential, if the client already sent that header (e.g., a placeholder),
// the proxy replaces it with the real value. When no placeholder matches,
// credentials are injected unconditionally for transparent auth. If multiple
// credentials share the same header name and no placeholder matched, the
// "claude" grant is skipped in favor of the other — claude uses OAuth and
// should only be injected when Claude Code explicitly sends a placeholder.
// When credentials have different header names, all are auto-injected.
// Returns a set of lower-cased header names that were injected, so callers
// can protect them from RemoveHeaders stripping.
func injectCredentials(req *http.Request, creds []credentialHeader, host, method, path string) map[string]bool {
	if len(creds) == 0 {
		return nil
	}

	injected := make(map[string]bool, len(creds))

	// First pass: inject credentials where the client sent a matching
	// placeholder header. This lets the client choose which credential
	// to use when multiple grants target the same host.
	for _, c := range creds {
		if req.Header.Get(c.Name) != "" {
			req.Header.Set(c.Name, c.Value)
			injected[strings.ToLower(c.Name)] = true
			slog.Debug("credential injected",
				"subsystem", "proxy",
				"action", "inject",
				"grant", c.Grant,
				"host", host,
				"header", c.Name,
				"method", method,
				"path", path)
		}
	}

	// If no placeholder matched, inject unconditionally for transparent auth.
	// When multiple credentials share the same header name, prefer the
	// non-"claude" grant — the claude grant is for Claude Code's OAuth flow
	// and should only be injected when explicitly requested via placeholder.
	if len(injected) == 0 {
		byHeader := make(map[string]credentialHeader, len(creds))
		for _, c := range creds {
			key := strings.ToLower(c.Name)
			if existing, ok := byHeader[key]; !ok || existing.Grant == "claude" {
				byHeader[key] = c
			}
		}
		for _, c := range byHeader {
			req.Header.Set(c.Name, c.Value)
			injected[strings.ToLower(c.Name)] = true
			slog.Debug("credential auto-injected",
				"subsystem", "proxy",
				"action", "inject-auto",
				"grant", c.Grant,
				"host", host,
				"header", c.Name,
				"method", method,
				"path", path)
		}
	}

	return injected
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
		slog.Debug("extra headers injected",
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
func (p *Proxy) getResponseTransformers(host string) []ResponseTransformer {
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

// getCredentialsForRequest returns all credentials for a host, checking
// RunContextData first, then falling back to the proxy's own map.
func (p *Proxy) getCredentialsForRequest(r *http.Request, host string) []credentialHeader {
	if rc := getRunContext(r); rc != nil {
		if creds := rc.Credentials[host]; len(creds) > 0 {
			return creds
		}
		h, _, _ := net.SplitHostPort(host)
		if h != "" {
			if creds := rc.Credentials[h]; len(creds) > 0 {
				return creds
			}
		}
		return nil
	}
	if creds := p.getCredentials(host); len(creds) > 0 {
		return creds
	}
	return nil
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
func (p *Proxy) getResponseTransformersForRequest(r *http.Request, host string) []ResponseTransformer {
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

// redactURLUserinfo removes userinfo (user:password@) from a URL-ish string
// before logging. The proxy URL carries a per-run auth token in the userinfo
// component (http://moat:TOKEN@host:port/...) and logging it verbatim would
// expose the token in debug output. Returns the input unchanged if there is
// no '@' before the first '/'.
func redactURLUserinfo(s string) string {
	schemeEnd := strings.Index(s, "://")
	if schemeEnd < 0 {
		return s
	}
	rest := s[schemeEnd+3:]
	slash := strings.IndexByte(rest, '/')
	authority := rest
	if slash >= 0 {
		authority = rest[:slash]
	}
	at := strings.IndexByte(authority, '@')
	if at < 0 {
		return s
	}
	return s[:schemeEnd+3] + "***@" + rest[at+1:]
}

// rewriteURLHost replaces the host in rawURL with newHost, preserving scheme,
// port, path, query, and fragment. Falls back to the original string on parse
// failure. Uses url.Parse rather than strings.Replace so bracketed IPv6 hosts
// like "http://[::1]:8080/path" rewrite to a valid URL (e.g. "http://127.0.0.1:8080/path"
// rather than "http://[127.0.0.1]:8080/path"), and so path or query text that
// happens to match the host literal is not corrupted.
func rewriteURLHost(rawURL, newHost string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	if port := u.Port(); port != "" {
		u.Host = net.JoinHostPort(newHost, port)
	} else {
		u.Host = newHost
	}
	return u.String()
}

// rewriteHostPort replaces the host portion of a "host:port" address with
// newHost, emitting bracketed form for IPv6 when necessary. Falls back to the
// original string on parse failure.
func rewriteHostPort(hostPort, newHost string) string {
	_, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		return hostPort
	}
	return net.JoinHostPort(newHost, port)
}

// isHostGateway returns true if the given host matches the run's host gateway address.
//
// Matches are:
//   - The literal HostGateway value (synthetic "moat-host" or the legacy
//     IP form "127.0.0.1" for pre-synthetic-hostname daemons).
//   - Loopback aliases ("localhost", "127.0.0.1", "::1") whenever HostGateway
//     is either the synthetic hostname or the legacy loopback IP. In both
//     configurations HostGatewayIP is 127.0.0.1, so a container CONNECT to
//     "localhost:8080" or "127.0.0.1:8080" would otherwise slip past the
//     host-service check and be dialed as-is, bypassing the per-port allowlist.
func isHostGateway(rc *RunContextData, host string) bool {
	if rc == nil || rc.HostGateway == "" {
		return false
	}
	if host == rc.HostGateway {
		return true
	}
	// Synthetic hostname or legacy loopback IP — both route to 127.0.0.1 on
	// the host side, so loopback aliases must be caught here.
	if rc.HostGateway == hostnames.HostGateway || rc.HostGateway == "127.0.0.1" {
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	}
	return false
}

// isAllowedHostPort returns true if the given port is in the run's allowed host ports list.
func isAllowedHostPort(rc *RunContextData, port int) bool {
	for _, p := range rc.AllowedHostPorts {
		if p == port {
			return true
		}
	}
	return false
}

// checkNetworkPolicyForRequest checks network policy using RunContextData first,
// then falling back to the proxy's own policy.
//
// For CONNECT requests, only host-level checking is performed. Per-path rules
// are enforced on the inner HTTP requests after TLS interception.
func (p *Proxy) checkNetworkPolicyForRequest(r *http.Request, host string, port int, method, path string) bool {
	if rc := getRunContext(r); rc != nil {
		// Block host-gateway traffic unless the port is explicitly allowed.
		if isHostGateway(rc, host) {
			return isAllowedHostPort(rc, port)
		}
		if method != "CONNECT" && rc.RequestCheck != nil {
			return rc.RequestCheck(host, port, method, path)
		}
		if rc.Policy != "strict" {
			return true
		}
		return matchHost(rc.AllowedHosts, host, port)
	}

	p.mu.RLock()
	checker := p.requestChecker
	p.mu.RUnlock()

	if method != "CONNECT" && checker != nil {
		return checker(host, port, method, path)
	}
	return p.checkNetworkPolicy(host, port)
}

// hasPathRulesForHost returns true if any matching host entry has per-path rules.
func (p *Proxy) hasPathRulesForHost(r *http.Request, host string, port int) bool {
	if rc := getRunContext(r); rc != nil {
		if rc.PathRulesCheck != nil {
			return rc.PathRulesCheck(host, port)
		}
		return false
	}
	p.mu.RLock()
	checker := p.pathRulesChecker
	p.mu.RUnlock()
	if checker != nil {
		return checker(host, port)
	}
	return false
}

// getMCPServersForRequest returns MCP servers from RunContextData or falls
// back to the proxy's own list.
func (p *Proxy) getMCPServersForRequest(r *http.Request) []MCPServerConfig {
	if rc := getRunContext(r); rc != nil {
		return rc.MCPServers
	}
	return p.mcpServers
}

// getCredStoreForRequest returns the credential store from RunContextData
// or falls back to the proxy's own store.
func (p *Proxy) getCredStoreForRequest(r *http.Request) CredentialStore {
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

// handleDirectMCPRelay handles MCP relay requests that arrive directly (not through proxy).
// URL format: /mcp/{token}/{server-name}[/path]
// Extracts the auth token from the URL, resolves run context, rewrites the path
// to strip the token, and dispatches to handleMCPRelay.
func (p *Proxy) handleDirectMCPRelay(w http.ResponseWriter, r *http.Request) {
	// Parse: /mcp/{token}/{name}[/subpath]
	rest := strings.TrimPrefix(r.URL.Path, "/mcp/")
	idx := strings.IndexByte(rest, '/')
	if idx < 0 {
		// No server name after token — malformed URL
		http.Error(w, "invalid MCP relay URL", http.StatusBadRequest)
		return
	}
	token := rest[:idx]
	remainder := rest[idx:] // starts with /, e.g. /server-name or /server-name/subpath

	rc, found := p.contextResolver(token)
	if !found {
		http.Error(w, "Invalid proxy token", http.StatusProxyAuthRequired)
		return
	}

	// Rewrite path to strip token: /mcp/{name}[/subpath]
	r.URL.Path = "/mcp" + remainder
	ctx := context.WithValue(r.Context(), runContextKey, rc)
	r = r.WithContext(ctx)
	p.handleMCPRelay(w, r)
}

// handleDirectAWSCredentials handles AWS credential endpoint requests that arrive
// directly from containers. The credential helper sends Authorization: Bearer {token}
// where token is the run's proxy auth token. We extract it to resolve run context,
// then dispatch to the per-run AWS handler.
func (p *Proxy) handleDirectAWSCredentials(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		http.Error(w, "Authorization required", http.StatusUnauthorized)
		return
	}
	token := auth[7:]

	rc, found := p.contextResolver(token)
	if !found {
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	if rc.AWSHandler == nil {
		http.Error(w, "AWS credentials not configured for this run", http.StatusNotFound)
		return
	}

	ctx := context.WithValue(r.Context(), runContextKey, rc)
	r = r.WithContext(ctx)
	rc.AWSHandler.ServeHTTP(w, r)
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

	// Direct MCP relay requests from containers (via NO_PROXY bypass).
	// URL format: /mcp/{token}/{server-name}[/path]
	// The auth token is embedded in the URL because direct requests don't carry
	// Proxy-Authorization. We resolve run context from the token, strip it from
	// the path, and dispatch to handleMCPRelay.
	if p.contextResolver != nil && r.URL.Host == "" && strings.HasPrefix(r.URL.Path, "/mcp/") {
		p.handleDirectMCPRelay(w, r)
		return
	}

	// Direct AWS credential endpoint requests from containers.
	// The credential helper sends Authorization: Bearer {token} (not Proxy-Authorization).
	// We extract the run's auth token from that header to resolve context.
	if p.contextResolver != nil && r.URL.Host == "" && strings.HasPrefix(r.URL.Path, "/_aws/") {
		p.handleDirectAWSCredentials(w, r)
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
		slog.Debug("proxy connect",
			"subsystem", "proxy",
			"action", "connect",
			"host", host,
			"port", port)
		p.handleConnect(w, r)
		return
	}

	slog.Debug("proxy request",
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
	w.Header().Set("X-Moat-Blocked", "request-rule")
	w.Header().Set("Proxy-Authenticate", "Moat-Policy")
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusProxyAuthRequired)
	_, _ = w.Write([]byte("Moat: request blocked by network policy.\nHost \"" + host + "\" is not in the allow list.\nAdd it to network.rules in moat.yaml or use policy: permissive.\n"))
}

// writeHostBlockedResponse writes a 407 response when a request to the host gateway is blocked.
func (p *Proxy) writeHostBlockedResponse(w http.ResponseWriter, host string, port int) {
	w.Header().Set("X-Moat-Blocked", "host-service")
	w.Header().Set("Proxy-Authenticate", "Moat-Policy")
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusProxyAuthRequired)
	_, _ = fmt.Fprintf(w, "Moat: request blocked — host service access to %s:%d is not allowed by default.\n"+
		"To allow port %d on the host, add to moat.yaml:\n\n"+
		"  network:\n    host:\n      - %d\n", host, port, port, port)
}

func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Extract host and infer port from scheme
	host := r.URL.Hostname()
	creds := p.getCredentialsForRequest(r, host)

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
	if !p.checkNetworkPolicyForRequest(r, host, port, r.Method, r.URL.Path) {
		duration := time.Since(start)
		// Log blocked request
		p.logRequest(r, r.Method, r.URL.String(), http.StatusProxyAuthRequired, duration, nil, originalReqHeaders, nil, reqBody, nil, nil)
		rc := getRunContext(r)
		if rc != nil && isHostGateway(rc, host) {
			p.logPolicy(r, "network", "http.request", "", "Host service blocked: "+host+":"+strconv.Itoa(port))
			p.writeHostBlockedResponse(w, host, port)
		} else {
			p.logPolicy(r, "network", "http.request", "", "Host not in allow list: "+host)
			p.writeBlockedResponse(w, host)
		}
		return
	}

	// Rewrite synthetic host-gateway hostname to actual IP for forwarding.
	// The container uses "moat-host" (which only exists in its /etc/hosts),
	// but the proxy runs on the host where that name doesn't resolve.
	outURL := r.URL.String()
	if rc := getRunContext(r); rc != nil && rc.HostGatewayIP != "" && isHostGateway(rc, host) {
		outURL = rewriteURLHost(outURL, rc.HostGatewayIP)
	}

	// Create outgoing request
	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, outURL, r.Body)
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
	injectedHeaders := injectCredentials(outReq, creds, host, r.Method, r.URL.Path)

	// Inject any additional headers configured for this host.
	// Merges with existing values (comma-separated) to preserve client
	// headers like anthropic-beta that support multiple flags.
	mergeExtraHeaders(outReq, host, p.getExtraHeadersForRequest(r, host))

	outReq.Header.Del("Proxy-Connection")
	outReq.Header.Del("Proxy-Authorization")

	// Remove headers that should be stripped, but never remove a
	// credential header the proxy just injected. This prevents conflicts
	// when multiple grants target the same host — e.g., "claude" registers
	// RemoveRequestHeader("x-api-key") for OAuth, but if "anthropic" also
	// injected x-api-key, the injected header must survive.
	for _, headerName := range p.getRemoveHeadersForRequest(r, host) {
		if injectedHeaders[strings.ToLower(headerName)] {
			continue
		}
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

	p.logRequest(r, r.Method, r.URL.String(), statusCode, duration, err, originalReqHeaders, respHeaders, reqBody, respBody, injectedHeaders)

	if err != nil {
		// Redact proxy userinfo from logged URLs so the per-run auth token
		// never lands in debug logs verbatim.
		slog.Warn("proxy forward failed",
			"subsystem", "proxy",
			"method", r.Method,
			"in_url", redactURLUserinfo(r.URL.String()),
			"out_url", redactURLUserinfo(outURL),
			"error", err.Error())
		// Don't echo the upstream error verbatim to the container — it can
		// leak internal hostnames/IPs and is rarely useful to the agent.
		http.Error(w, "moat proxy: upstream request failed", http.StatusBadGateway)
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
	if !p.checkNetworkPolicyForRequest(r, host, port, "CONNECT", "") {
		// Log blocked request
		if p.logger != nil {
			p.logRequest(r, r.Method, r.Host, http.StatusProxyAuthRequired, 0, nil, nil, nil, nil, nil, nil)
		}
		rc := getRunContext(r)
		if rc != nil && isHostGateway(rc, host) {
			p.logPolicy(r, "network", "http.connect", "", "Host service blocked: "+host+":"+strconv.Itoa(port))
			p.writeHostBlockedResponse(w, host, port)
		} else {
			p.logPolicy(r, "network", "http.connect", "", "Host not in allow list: "+host)
			p.writeBlockedResponse(w, host)
		}
		return
	}

	// Do MITM interception when we have a CA configured.
	//
	// Security note: This intercepts ALL HTTPS traffic, not just credential-injected hosts.
	// This is intentional for full observability - a core Moat feature. The container
	// trusts our CA (mounted at /etc/ssl/certs/moat-ca/) and we verify upstream certs.
	//
	// Applications with certificate pinning may fail. This is expected behavior since
	// observability requires seeing all traffic.
	if p.ca != nil {
		p.handleConnectWithInterception(w, r, host)
		return
	}

	// Without TLS interception, per-path rules cannot be enforced on HTTPS
	// traffic — only host-level allow/deny applies. Warn if rules exist.
	if p.hasPathRulesForHost(r, host, port) {
		slog.Warn("per-path rules configured but TLS interception disabled; only host-level rules apply",
			"subsystem", "proxy", "host", host)
	}

	p.handleConnectTunnel(w, r)
}

// connectTunnelDialTimeout bounds how long the proxy waits to connect to the
// upstream on behalf of a CONNECT request. An unreachable HostGatewayIP (e.g.
// daemon-version-skew sends an empty IP and the fallback resolves nowhere)
// would otherwise cause the container to stall indefinitely.
const connectTunnelDialTimeout = 10 * time.Second

func (p *Proxy) handleConnectTunnel(w http.ResponseWriter, r *http.Request) {
	// Rewrite synthetic host-gateway hostname to actual IP for dialing.
	dialAddr := r.Host
	if rc := getRunContext(r); rc != nil && rc.HostGatewayIP != "" {
		host, _, _ := net.SplitHostPort(r.Host)
		if isHostGateway(rc, host) {
			dialAddr = rewriteHostPort(r.Host, rc.HostGatewayIP)
		}
	}
	targetConn, err := net.DialTimeout("tcp", dialAddr, connectTunnelDialTimeout)
	if err != nil {
		http.Error(w, "moat proxy: dial upstream failed", http.StatusBadGateway)
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
	creds := p.getCredentialsForRequest(r, host)

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
		slog.Debug("failed to generate cert for CONNECT interception",
			"subsystem", "proxy", "host", host, "error", err)
		return
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*cert},
		MinVersion:   tls.VersionTLS12,
	}
	tlsClientConn := tls.Server(clientConn, tlsConfig)
	if err := tlsClientConn.Handshake(); err != nil {
		slog.Debug("TLS handshake failed during CONNECT interception",
			"subsystem", "proxy", "host", host, "error", err)
		return
	}
	defer tlsClientConn.Close()

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    p.upstreamCAs, // nil means system roots
		},
		MaxIdleConns:    100,
		IdleConnTimeout: 90 * time.Second,
		// Note: Do NOT set ForceAttemptHTTP2 here. This transport forwards
		// HTTP/1.1 requests read from the intercepted TLS connection. Enabling
		// HTTP/2 on the upstream side causes framing mismatches and hangs.
	}

	// Extract port from the CONNECT request for rule checking.
	// Defaults to 443 since this is a TLS-intercepted connection.
	connectPort := 443
	if _, pStr, err := net.SplitHostPort(r.Host); err == nil {
		if p, err := net.LookupPort("tcp", pStr); err == nil {
			connectPort = p
		}
	}

	clientReader := bufio.NewReader(tlsClientConn)
	for {
		req, err := http.ReadRequest(clientReader)
		if err != nil {
			if err != io.EOF {
				slog.Debug("failed to read request from intercepted connection",
					"subsystem", "proxy", "host", host, "error", err)
			}
			return
		}

		// Capture request body and headers
		var reqBody []byte
		reqBody, req.Body = captureBody(req.Body, req.Header.Get("Content-Type"))
		originalReqHeaders := req.Header.Clone()

		req.URL.Scheme = "https"
		// Rewrite synthetic host-gateway hostname to actual IP for forwarding.
		connectHost := r.Host
		if rc := getRunContext(r); rc != nil && rc.HostGatewayIP != "" && isHostGateway(rc, host) {
			connectHost = rewriteHostPort(r.Host, rc.HostGatewayIP)
		}
		req.URL.Host = connectHost
		req.RequestURI = ""

		// Check request-level rules (method + path) for the inner HTTP request.
		// The CONNECT request r carries the per-run context for rule lookup.
		if !p.checkNetworkPolicyForRequest(r, host, connectPort, req.Method, req.URL.Path) {
			p.logRequest(r, req.Method, req.URL.String(), http.StatusProxyAuthRequired, 0, nil, nil, nil, nil, nil, nil)
			p.logPolicy(r, "network", "http.request", "", req.Method+" "+host+req.URL.Path)
			body := "Moat: request blocked by network policy.\nHost: " + host + "\nTo allow this request, update network.rules in moat.yaml.\n"
			blockedResp := &http.Response{
				StatusCode:    http.StatusProxyAuthRequired,
				ProtoMajor:    1,
				ProtoMinor:    1,
				Header:        make(http.Header),
				ContentLength: int64(len(body)),
				Body:          io.NopCloser(strings.NewReader(body)),
			}
			blockedResp.Header.Set("X-Moat-Blocked", "request-rule")
			blockedResp.Header.Set("Content-Type", "text/plain")
			_ = blockedResp.Write(tlsClientConn)
			continue
		}

		// Evaluate Keep policy for the inner HTTP request.
		// Uses the global "http" engine from network.keep_policy.
		if rc := getRunContext(r); rc != nil && rc.KeepEngines != nil {
			scope := "http"
			if eng, ok := rc.KeepEngines[scope]; ok {
				call := keeplib.NewHTTPCall(req.Method, host, req.URL.Path)
				call.Context.Scope = "http-" + host
				result, evalErr := keeplib.SafeEvaluate(eng, call, scope)
				if evalErr != nil {
					slog.Warn("Keep evaluation error for HTTP request, denying (fail-closed)",
						"host", host,
						"method", req.Method,
						"path", req.URL.Path,
						"error", evalErr)
					p.logPolicy(r, scope, "http.request", "evaluation-error", "Policy evaluation failed")
					msg := "Moat: request blocked — policy evaluation error.\nHost: " + host + "\n"
					blockedResp := &http.Response{
						StatusCode:    http.StatusForbidden,
						ProtoMajor:    1,
						ProtoMinor:    1,
						Header:        make(http.Header),
						ContentLength: int64(len(msg)),
						Body:          io.NopCloser(strings.NewReader(msg)),
					}
					blockedResp.Header.Set("X-Moat-Blocked", "keep-policy")
					blockedResp.Header.Set("Content-Type", "text/plain")
					_ = blockedResp.Write(tlsClientConn)
					continue
				} else if result.Decision == keeplib.Deny {
					p.logPolicy(r, scope, "http.request", result.Rule, result.Message)
					msg := "Moat: request blocked by Keep policy.\nHost: " + host + "\n"
					if result.Message != "" {
						msg += result.Message + "\n"
					}
					blockedResp := &http.Response{
						StatusCode:    http.StatusForbidden,
						ProtoMajor:    1,
						ProtoMinor:    1,
						Header:        make(http.Header),
						ContentLength: int64(len(msg)),
						Body:          io.NopCloser(strings.NewReader(msg)),
					}
					blockedResp.Header.Set("X-Moat-Blocked", "keep-policy")
					blockedResp.Header.Set("Content-Type", "text/plain")
					_ = blockedResp.Write(tlsClientConn)
					continue
				}
			}
		}

		// Inject MCP credentials if this is an MCP request.
		// Use the CONNECT request r for context lookups since inner
		// requests from the TLS stream don't carry the request context.
		p.injectMCPCredentialsWithContext(r, req)

		injectedHeaders := injectCredentials(req, creds, host, req.Method, req.URL.Path)

		// Inject any additional headers configured for this host.
		// Merges with existing values (comma-separated) to preserve client
		// headers like anthropic-beta that support multiple flags.
		mergeExtraHeaders(req, r.Host, p.getExtraHeadersForRequest(r, r.Host))
		req.Header.Del("Proxy-Connection")
		req.Header.Del("Proxy-Authorization")

		// Remove headers that should be stripped for this host, but never
		// remove a credential header the proxy just injected (see comment
		// in handleHTTP for the multi-grant conflict scenario).
		for _, headerName := range p.getRemoveHeadersForRequest(r, host) {
			if injectedHeaders[strings.ToLower(headerName)] {
				continue
			}
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

		// Evaluate LLM gateway policy on Anthropic API responses.
		// NOTE: Only applies to the default Anthropic endpoint. Custom
		// ANTHROPIC_BASE_URL endpoints bypass policy evaluation — this is
		// mutually exclusive with llm-gateway (see config validation).
		if resp != nil && resp.StatusCode == http.StatusOK && host == "api.anthropic.com" {
			if rc := getRunContext(r); rc != nil && rc.KeepEngines != nil {
				if eng, ok := rc.KeepEngines["llm-gateway"]; ok {
					respBodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseSize+1))
					resp.Body.Close()
					if readErr != nil {
						slog.Warn("failed to read response body for LLM policy, denying (fail-closed)",
							"host", host, "error", readErr)
						p.logPolicy(r, "llm-gateway", "llm.read_error", "read-error", "Failed to read response body for policy evaluation")
						errorBody := buildPolicyDeniedResponse("read-error", "Failed to read response body for policy evaluation.")
						resp = &http.Response{
							StatusCode:    http.StatusBadRequest,
							ProtoMajor:    1,
							ProtoMinor:    1,
							Header:        make(http.Header),
							ContentLength: int64(len(errorBody)),
							Body:          io.NopCloser(bytes.NewReader(errorBody)),
						}
						resp.Header.Set("Content-Type", "application/json")
						resp.Header.Set("X-Moat-Blocked", "llm-policy")
					} else if int64(len(respBodyBytes)) > maxLLMResponseSize {
						// Response exceeds size limit — deny (fail-closed).
						slog.Warn("LLM response exceeds max size for policy evaluation, denying (fail-closed)",
							"size", len(respBodyBytes), "limit", maxLLMResponseSize)
						p.logPolicy(r, "llm-gateway", "llm.response_too_large", "size-limit", "Response too large for policy evaluation")
						errorBody := buildPolicyDeniedResponse("size-limit", "Response too large for policy evaluation.")
						resp = &http.Response{
							StatusCode:    http.StatusBadRequest,
							ProtoMajor:    1,
							ProtoMinor:    1,
							Header:        make(http.Header),
							ContentLength: int64(len(errorBody)),
							Body:          io.NopCloser(bytes.NewReader(errorBody)),
						}
						resp.Header.Set("Content-Type", "application/json")
						resp.Header.Set("X-Moat-Blocked", "llm-policy")
					} else {
						result := evaluateLLMResponse(eng, respBodyBytes, resp)
						if result.Denied {
							slog.Info("LLM tool_use denied by policy",
								"rule", result.Rule, "message", result.Message)
							p.logPolicy(r, "llm-gateway", "llm.tool_use", result.Rule, result.Message)
							errorBody := buildPolicyDeniedResponse(result.Rule, result.Message)
							resp = &http.Response{
								StatusCode:    http.StatusBadRequest,
								ProtoMajor:    1,
								ProtoMinor:    1,
								Header:        make(http.Header),
								ContentLength: int64(len(errorBody)),
								Body:          io.NopCloser(bytes.NewReader(errorBody)),
							}
							resp.Header.Set("Content-Type", "application/json")
							resp.Header.Set("X-Moat-Blocked", "llm-policy")
						} else if result.Events != nil {
							// SSE response allowed — re-serialize evaluated events.
							// Events were decompressed for evaluation, so the
							// re-serialized body is plaintext — remove Content-Encoding.
							var buf bytes.Buffer
							for _, ev := range result.Events {
								if ev.ID != "" {
									fmt.Fprintf(&buf, "id: %s\n", ev.ID)
								}
								if ev.Type != "" {
									fmt.Fprintf(&buf, "event: %s\n", ev.Type)
								}
								// Per SSE spec, multi-line data needs a `data:` prefix per line.
								lines := strings.Split(ev.Data, "\n")
								for _, line := range lines {
									fmt.Fprintf(&buf, "data: %s\n", line)
								}
								buf.WriteByte('\n') // Event terminator.
							}
							resp.Header.Del("Content-Encoding")
							resp.Body = io.NopCloser(&buf)
							resp.ContentLength = int64(buf.Len())
						} else {
							// JSON response allowed — restore original body.
							resp.Body = io.NopCloser(bytes.NewReader(respBodyBytes))
							resp.ContentLength = int64(len(respBodyBytes))
						}
					}
				}
			}
		}

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

		p.logRequest(r, req.Method, logURL, statusCode, duration, err, originalReqHeaders, respHeaders, reqBody, respBody, injectedHeaders)

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
