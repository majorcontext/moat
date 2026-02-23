package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/mcpoauth"
)

// mcpRelayClient is a reused HTTP client for MCP relay requests.
// It bypasses proxy settings to prevent circular proxy loops.
var mcpRelayClient = &http.Client{
	Transport: &http.Transport{
		Proxy: nil, // Disable proxy - connect directly to MCP server
	},
}

// injectMCPCredentials checks if the request is to an MCP server and injects
// the real credential if a stub is detected.
func (p *Proxy) injectMCPCredentials(req *http.Request) {
	if len(p.mcpServers) == 0 {
		return
	}

	// Parse request URL to get host
	reqHost := req.URL.Host
	if reqHost == "" {
		reqHost = req.Host
	}

	// Find matching MCP server by host
	var matchedServer *config.MCPServerConfig
	for i := range p.mcpServers {
		server := &p.mcpServers[i]
		if server.Auth == nil {
			continue // No auth required
		}

		// Parse server URL to get host
		serverURL, err := url.Parse(server.URL)
		if err != nil {
			log.Debug("Failed to parse MCP server URL",
				"server", server.Name,
				"url", server.URL,
				"error", err)
			continue
		}

		// Match by host
		if serverURL.Host == reqHost {
			matchedServer = server
			break
		}
	}

	if matchedServer == nil {
		return // No matching MCP server
	}

	// Determine the header to check
	header := mcpAuthHeader(matchedServer.Auth)

	// Check if the specified header exists
	headerValue := req.Header.Get(header)

	if headerValue == "" {
		log.Debug("MCP: header not present in request",
			"server", matchedServer.Name,
			"header", header)
		return // Header not present
	}

	// Check if header value is a stub
	expectedStub := "moat-stub-" + matchedServer.Auth.Grant
	if headerValue != expectedStub {
		// Not a stub - could be a real credential or different value
		// Log debug if it looks like a stub but doesn't match
		if strings.HasPrefix(headerValue, "moat-stub-") {
			log.Debug("MCP request has stub-like header value that doesn't match expected grant",
				"server", matchedServer.Name,
				"header", header,
				"expected", expectedStub,
				"got", headerValue[:min(20, len(headerValue))]+"...")
		} else {
			log.Debug("MCP header has non-stub value",
				"server", matchedServer.Name,
				"header", header,
				"value", headerValue[:min(20, len(headerValue))]+"...")
		}
		return
	}

	// Load real credential
	cred, err := p.credStore.Get(credential.Provider(matchedServer.Auth.Grant))
	if err != nil {
		log.Error("MCP credential load failed",
			"subsystem", "proxy",
			"action", "inject-error",
			"server", matchedServer.Name,
			"grant", matchedServer.Auth.Grant,
			"error", err,
			"fix", "Run: moat grant mcp "+strings.TrimPrefix(matchedServer.Auth.Grant, "mcp-"))
		// Leave stub in place - request will fail with stub credential
		return
	}

	// For OAuth, attempt token refresh if expired
	token := cred.Token
	if mcpAuthType(matchedServer.Auth) == "oauth" {
		token = p.resolveOAuthToken(req.Context(), matchedServer, cred)
	}

	// Format the header value based on auth type
	headerVal := formatMCPToken(matchedServer.Auth, token)
	req.Header.Set(header, headerVal)

	log.Debug("credential injected",
		"subsystem", "proxy",
		"action", "inject",
		"grant", matchedServer.Auth.Grant,
		"host", reqHost,
		"header", header,
		"server", matchedServer.Name,
		"auth_type", mcpAuthType(matchedServer.Auth),
		"path", req.URL.Path)
}

// handleMCPRelay proxies MCP requests directly through the proxy with credential injection.
// Path format: /mcp/{server-name}
// This allows MCP clients that don't respect HTTP_PROXY to connect directly to the proxy.
func (p *Proxy) handleMCPRelay(w http.ResponseWriter, r *http.Request) {
	// Extract server name from path: /mcp/context7 -> context7
	serverName := strings.TrimPrefix(r.URL.Path, "/mcp/")
	if idx := strings.IndexByte(serverName, '/'); idx >= 0 {
		serverName = serverName[:idx]
	}

	// Find the MCP server config
	var mcpServer *config.MCPServerConfig
	for i := range p.mcpServers {
		if p.mcpServers[i].Name == serverName {
			mcpServer = &p.mcpServers[i]
			break
		}
	}

	if mcpServer == nil {
		// Include diagnostic info in error that will show up in Claude Code
		http.Error(w, fmt.Sprintf("MOAT: MCP server '%s' not configured. Available servers: %d. Check agent.yaml.",
			serverName, len(p.mcpServers)), http.StatusNotFound)
		return
	}

	// Build target URL by replacing /mcp/{server-name} with the actual MCP server URL
	targetURL, err := url.Parse(mcpServer.URL)
	if err != nil {
		http.Error(w, fmt.Sprintf("MOAT: Invalid MCP server URL for '%s': %s", serverName, mcpServer.URL),
			http.StatusInternalServerError)
		return
	}

	// Preserve any path after /mcp/{server-name}
	// e.g., /mcp/context7/v1/endpoint -> https://mcp.context7.com/mcp/v1/endpoint
	relPath := strings.TrimPrefix(r.URL.Path, "/mcp/"+serverName)
	if relPath != "" && relPath != "/" {
		targetURL.Path = strings.TrimSuffix(targetURL.Path, "/") + relPath
	}

	// Preserve query string
	targetURL.RawQuery = r.URL.RawQuery

	// Create new request to target with context
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL.String(), r.Body)
	if err != nil {
		http.Error(w, "Failed to create proxy request", http.StatusInternalServerError)
		return
	}

	// Copy headers
	for key, values := range r.Header {
		// Skip proxy-specific headers
		if key == "Proxy-Authorization" || key == "Proxy-Connection" {
			continue
		}
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}

	// Inject credentials
	if mcpServer.Auth != nil {
		if p.credStore == nil {
			http.Error(w, "MOAT: Credential store not initialized", http.StatusInternalServerError)
			return
		}

		cred, credErr := p.credStore.Get(credential.Provider(mcpServer.Auth.Grant))
		if credErr != nil {
			http.Error(w, fmt.Sprintf("MOAT: Failed to load credential for '%s'. Grant: %s. Run: moat grant mcp %s",
				serverName, mcpServer.Auth.Grant, strings.TrimPrefix(mcpServer.Auth.Grant, "mcp-")),
				http.StatusInternalServerError)
			return
		}

		// Resolve token (refresh if OAuth and expired)
		token := cred.Token
		if mcpAuthType(mcpServer.Auth) == "oauth" {
			token = p.resolveOAuthToken(r.Context(), mcpServer, cred)
		}

		// Inject the credential with proper formatting
		header := mcpAuthHeader(mcpServer.Auth)
		proxyReq.Header.Set(header, formatMCPToken(mcpServer.Auth, token))
	}

	// Send request to actual MCP server using the reused client
	resp, err := mcpRelayClient.Do(proxyReq)
	if err != nil {
		http.Error(w, fmt.Sprintf("MOAT: Failed to connect to MCP server '%s' at %s: %v",
			serverName, targetURL.String(), err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Copy status code
	w.WriteHeader(resp.StatusCode)

	// For SSE streaming, flush after headers
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	// Copy response body with streaming support
	_, _ = io.Copy(w, resp.Body)
}

// mcpAuthType returns the auth type for an MCP server, defaulting to "token".
func mcpAuthType(auth *config.MCPAuthConfig) string {
	if auth.Type == "" {
		return "token"
	}
	return auth.Type
}

// mcpAuthHeader returns the HTTP header to use for credential injection.
// For OAuth, defaults to "Authorization" if no header is specified.
// For other auth types, returns the configured header (may be empty).
func mcpAuthHeader(auth *config.MCPAuthConfig) string {
	if auth.Header != "" {
		return auth.Header
	}
	if mcpAuthType(auth) == "oauth" {
		return "Authorization"
	}
	return ""
}

// formatMCPToken formats a token for injection into the HTTP header.
// For OAuth, the token is prefixed with "Bearer ".
// For static tokens, the token is used as-is.
func formatMCPToken(auth *config.MCPAuthConfig, token string) string {
	if mcpAuthType(auth) == "oauth" {
		return "Bearer " + token
	}
	return token
}

// resolveOAuthToken returns the access token, refreshing it if expired.
// If refresh fails, returns the existing (possibly expired) token.
//
// Note: concurrent requests for the same expired token may each trigger a
// refresh independently. This is harmless (last writer wins in the credential
// store) but slightly wasteful. A singleflight could coalesce them.
func (p *Proxy) resolveOAuthToken(ctx context.Context, server *config.MCPServerConfig, cred *credential.Credential) string {
	// Check if token is still valid (with 60s buffer)
	if !cred.ExpiresAt.IsZero() && time.Until(cred.ExpiresAt) > 60*time.Second {
		return cred.Token
	}

	// Check if we have a refresh token
	refreshToken := ""
	if cred.Metadata != nil {
		refreshToken = cred.Metadata["refresh_token"]
	}
	if refreshToken == "" {
		log.Debug("MCP OAuth token expired but no refresh token available",
			"server", server.Name,
			"expires_at", cred.ExpiresAt)
		return cred.Token
	}

	// Get OAuth config from either the credential metadata or the server config
	tokenURL := server.Auth.TokenURL
	clientID := server.Auth.ClientID
	if tokenURL == "" && cred.Metadata != nil {
		tokenURL = cred.Metadata["token_url"]
	}
	if clientID == "" && cred.Metadata != nil {
		clientID = cred.Metadata["client_id"]
	}

	if tokenURL == "" || clientID == "" {
		log.Debug("MCP OAuth token expired but missing token_url or client_id for refresh",
			"server", server.Name,
			"has_token_url", tokenURL != "",
			"has_client_id", clientID != "")
		return cred.Token
	}

	// Attempt refresh
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	newToken, err := mcpoauth.RefreshAccessToken(ctx, tokenURL, clientID, refreshToken)
	if err != nil {
		log.Error("MCP OAuth token refresh failed",
			"server", server.Name,
			"error", err,
			"fix", "Run: moat grant mcp "+strings.TrimPrefix(server.Auth.Grant, "mcp-")+" --oauth")
		return cred.Token
	}

	// Update the stored credential with the new token
	cred.Token = newToken.AccessToken
	if !newToken.ExpiresAt.IsZero() {
		cred.ExpiresAt = newToken.ExpiresAt
	}
	if newToken.RefreshToken != "" && cred.Metadata != nil {
		cred.Metadata["refresh_token"] = newToken.RefreshToken
	}

	// Persist the refreshed credential
	if err := p.credStore.Save(*cred); err != nil {
		log.Error("Failed to save refreshed MCP OAuth token",
			"server", server.Name,
			"error", err)
	} else {
		log.Debug("MCP OAuth token refreshed",
			"server", server.Name,
			"expires_at", cred.ExpiresAt)
	}

	return newToken.AccessToken
}
