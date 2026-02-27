package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
)

// mcpRelayClient is a reused HTTP client for MCP relay requests.
// It bypasses proxy settings to prevent circular proxy loops.
var mcpRelayClient = &http.Client{
	Transport: &http.Transport{
		Proxy: nil, // Disable proxy - connect directly to MCP server
	},
}

// injectMCPCredentials checks if the request is to an MCP server and injects
// the real credential if a stub is detected. Uses the request's own context
// for RunContextData lookup.
func (p *Proxy) injectMCPCredentials(req *http.Request) {
	p.injectMCPCredentialsWithContext(req, req)
}

// injectMCPCredentialsWithContext checks if the target request is to an MCP
// server and injects the real credential if a stub is detected.
// The ctxReq parameter provides the RunContextData (from CONNECT request context),
// while targetReq is the actual request being modified.
func (p *Proxy) injectMCPCredentialsWithContext(ctxReq, targetReq *http.Request) {
	mcpServers := p.getMCPServersForRequest(ctxReq)
	if len(mcpServers) == 0 {
		return
	}

	credStore := p.getCredStoreForRequest(ctxReq)

	// Parse request URL to get host
	reqHost := targetReq.URL.Host
	if reqHost == "" {
		reqHost = targetReq.Host
	}

	// Find matching MCP server by host
	var matchedServer *config.MCPServerConfig
	for i := range mcpServers {
		server := &mcpServers[i]
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

	// Check if the specified header exists
	headerValue := targetReq.Header.Get(matchedServer.Auth.Header)

	if headerValue == "" {
		log.Debug("MCP: header not present in request",
			"server", matchedServer.Name,
			"header", matchedServer.Auth.Header)
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
				"header", matchedServer.Auth.Header,
				"expected", expectedStub,
				"got", headerValue[:min(20, len(headerValue))]+"...")
		} else {
			log.Debug("MCP header has non-stub value",
				"server", matchedServer.Name,
				"header", matchedServer.Auth.Header,
				"value", headerValue[:min(20, len(headerValue))]+"...")
		}
		return
	}

	// Load real credential. In daemon mode, credentials are pre-resolved in
	// RunContextData.Credentials (keyed by host, with Grant field).
	var credValue string
	if rc := getRunContext(ctxReq); rc != nil {
		for _, c := range rc.Credentials {
			if c.Grant == matchedServer.Auth.Grant {
				credValue = c.Value
				break
			}
		}
	}
	if credValue == "" && credStore != nil {
		cred, err := credStore.Get(credential.Provider(matchedServer.Auth.Grant))
		if err == nil {
			credValue = cred.Token
		}
	}
	if credValue == "" {
		log.Error("MCP credential load failed",
			"subsystem", "proxy",
			"action", "inject-error",
			"server", matchedServer.Name,
			"grant", matchedServer.Auth.Grant,
			"fix", "Run: moat grant mcp "+strings.TrimPrefix(matchedServer.Auth.Grant, "mcp-"))
		// Leave stub in place - request will fail with stub credential
		return
	}

	// Replace stub with real credential
	targetReq.Header.Set(matchedServer.Auth.Header, credValue)

	log.Debug("credential injected",
		"subsystem", "proxy",
		"action", "inject",
		"grant", matchedServer.Auth.Grant,
		"host", reqHost,
		"header", matchedServer.Auth.Header,
		"server", matchedServer.Name,
		"path", targetReq.URL.Path)
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

	mcpServers := p.getMCPServersForRequest(r)
	credStore := p.getCredStoreForRequest(r)

	// Find the MCP server config
	var mcpServer *config.MCPServerConfig
	for i := range mcpServers {
		if mcpServers[i].Name == serverName {
			mcpServer = &mcpServers[i]
			break
		}
	}

	if mcpServer == nil {
		// Include diagnostic info in error that will show up in Claude Code
		http.Error(w, fmt.Sprintf("MOAT: MCP server '%s' not configured. Available servers: %d. Check agent.yaml.",
			serverName, len(mcpServers)), http.StatusNotFound)
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

	// Inject credentials.
	// In daemon mode, credentials are pre-resolved in RunContextData.Credentials
	// (keyed by host, with Grant field). Try that first, then fall back to credStore.
	if mcpServer.Auth != nil {
		var credValue string

		// Try RunContextData credentials (daemon mode).
		if rc := getRunContext(r); rc != nil {
			for _, cred := range rc.Credentials {
				if cred.Grant == mcpServer.Auth.Grant {
					credValue = cred.Value
					break
				}
			}
		}

		// Fall back to credential store (legacy single-run mode).
		if credValue == "" && credStore != nil {
			cred, credErr := credStore.Get(credential.Provider(mcpServer.Auth.Grant))
			if credErr == nil {
				credValue = cred.Token
			}
		}

		if credValue == "" {
			http.Error(w, fmt.Sprintf("MOAT: Failed to load credential for '%s'. Grant: %s. Run: moat grant mcp %s",
				serverName, mcpServer.Auth.Grant, strings.TrimPrefix(mcpServer.Auth.Grant, "mcp-")),
				http.StatusInternalServerError)
			return
		}

		// Inject the real credential
		proxyReq.Header.Set(mcpServer.Auth.Header, credValue)
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
