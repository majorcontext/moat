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
			log.Warn("Failed to parse MCP server URL",
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
	headerValue := req.Header.Get(matchedServer.Auth.Header)

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
		// Log warning if it looks like a stub but doesn't match
		if strings.HasPrefix(headerValue, "moat-stub-") {
			log.Warn("MCP request has stub-like header value that doesn't match expected grant",
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

	// Replace stub with real credential
	req.Header.Set(matchedServer.Auth.Header, cred.Token)

	log.Debug("credential injected",
		"subsystem", "proxy",
		"action", "inject",
		"grant", matchedServer.Auth.Grant,
		"host", reqHost,
		"header", matchedServer.Auth.Header,
		"server", matchedServer.Name,
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

		// Inject the real credential
		proxyReq.Header.Set(mcpServer.Auth.Header, cred.Token)
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
