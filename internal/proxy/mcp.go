package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	keeplib "github.com/majorcontext/keep"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	internalkeep "github.com/majorcontext/moat/internal/keep"
)

// findCredByGrant searches all credentials for one matching the given grant
// name and returns its value. Returns "" if no match is found.
func findCredByGrant(creds map[string][]credentialHeader, grant string) string {
	for _, cs := range creds {
		for _, c := range cs {
			if c.Grant == grant {
				return c.Value
			}
		}
	}
	return ""
}

// mcpRelayClient is a reused HTTP client for MCP relay requests.
// It bypasses proxy settings to prevent circular proxy loops.
var mcpRelayClient = &http.Client{
	Transport: &http.Transport{
		Proxy: nil, // Disable proxy - connect directly to MCP server
	},
}

// formatCredValue prepends "Bearer " for OAuth grants; returns raw value otherwise.
// grantToCommand converts a grant name like "oauth:notion" or "mcp-context7"
// to a CLI-friendly form suitable for use in "moat grant <args>" instructions.
// Examples: "oauth:notion" → "oauth notion", "mcp-context7" → "mcp context7".
func grantToCommand(grant string) string {
	if parts := strings.SplitN(grant, ":", 2); len(parts) == 2 {
		return parts[0] + " " + parts[1]
	}
	if after, ok := strings.CutPrefix(grant, "mcp-"); ok {
		return "mcp " + after
	}
	return grant
}

func formatCredValue(grant, value string) string {
	if strings.HasPrefix(grant, "oauth:") {
		return "Bearer " + value
	}
	return value
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
			slog.Debug("Failed to parse MCP server URL",
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
		slog.Debug("MCP: header not present in request",
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
			slog.Debug("MCP request has stub-like header value that doesn't match expected grant",
				"server", matchedServer.Name,
				"header", matchedServer.Auth.Header,
				"expected", expectedStub,
				"got", headerValue[:min(20, len(headerValue))]+"...")
		} else {
			slog.Debug("MCP header has non-stub value",
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
		credValue = findCredByGrant(rc.Credentials, matchedServer.Auth.Grant)
	}
	if credValue == "" && credStore != nil {
		cred, err := credStore.Get(credential.Provider(matchedServer.Auth.Grant))
		if err == nil {
			credValue = cred.Token
		}
	}
	if credValue == "" {
		slog.Error("MCP credential load failed",
			"subsystem", "proxy",
			"action", "inject-error",
			"server", matchedServer.Name,
			"grant", matchedServer.Auth.Grant,
			"fix", "Run: moat grant "+grantToCommand(matchedServer.Auth.Grant))
		// Leave stub in place - request will fail with stub credential
		return
	}

	// Replace stub with real credential
	targetReq.Header.Set(matchedServer.Auth.Header, formatCredValue(matchedServer.Auth.Grant, credValue))

	slog.Debug("credential injected",
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
		http.Error(w, fmt.Sprintf("MOAT: MCP server '%s' not configured. Available servers: %d. Check moat.yaml.",
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

	// Evaluate Keep policy for MCP tool calls before consuming the body.
	// Engine key uses "mcp-" prefix to avoid collisions with "http" and "llm-gateway" keys.
	if rc := getRunContext(r); rc != nil && rc.KeepEngines != nil {
		if eng, ok := rc.KeepEngines["mcp-"+serverName]; ok {
			bodyBytes, readErr := io.ReadAll(r.Body)
			if readErr != nil {
				http.Error(w, "Failed to read request body", http.StatusInternalServerError)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

			var mcpReq struct {
				Method string `json:"method"`
				Params struct {
					Name      string         `json:"name"`
					Arguments map[string]any `json:"arguments"`
				} `json:"params"`
			}
			if unmarshalErr := json.Unmarshal(bodyBytes, &mcpReq); unmarshalErr != nil {
				// Fail-closed: deny non-JSON requests when a policy is configured.
				slog.Warn("MCP request body is not valid JSON, denying (fail-closed)",
					"server", serverName, "error", unmarshalErr)
				http.Error(w, "Moat: MCP request blocked — invalid JSON body.", http.StatusForbidden)
				return
			}
			if mcpReq.Method == "tools/call" && mcpReq.Params.Name != "" {
				scope := "mcp-" + serverName
				call := internalkeep.NormalizeMCPCall(mcpReq.Params.Name, mcpReq.Params.Arguments, scope)
				result, evalErr := internalkeep.SafeEvaluate(eng, call, scope)
				if evalErr != nil {
					slog.Warn("Keep evaluation error for MCP call, denying (fail-closed)",
						"server", serverName,
						"tool", mcpReq.Params.Name,
						"error", evalErr)
					p.logPolicy(r, scope, "mcp.tool_call", "evaluation-error", "Policy evaluation failed")
					http.Error(w, "Moat: MCP tool call blocked — policy evaluation error.", http.StatusForbidden)
					return
				}
				switch result.Decision {
				case keeplib.Deny:
					p.logPolicy(r, scope, "mcp.tool_call", result.Rule, result.Message)
					msg := "Moat: MCP tool call blocked by policy."
					if result.Message != "" {
						msg += " " + result.Message
					}
					http.Error(w, msg, http.StatusForbidden)
					return
				case keeplib.Redact:
					mutated := keeplib.ApplyMutations(mcpReq.Params.Arguments, result.Mutations)
					// Re-encode the body with mutated arguments.
					var raw map[string]any
					if unmarshalErr := json.Unmarshal(bodyBytes, &raw); unmarshalErr != nil {
						slog.Warn("failed to unmarshal MCP body for redaction, denying (fail-closed)",
							"server", serverName, "error", unmarshalErr)
						p.logPolicy(r, scope, "mcp.tool_call", "redaction-error", "Failed to redact request")
						http.Error(w, "Moat: MCP tool call blocked — redaction failed.", http.StatusForbidden)
						return
					}
					params, ok := raw["params"].(map[string]any)
					if !ok {
						slog.Warn("MCP body missing params map for redaction, denying (fail-closed)",
							"server", serverName)
						p.logPolicy(r, scope, "mcp.tool_call", "redaction-error", "Failed to redact request")
						http.Error(w, "Moat: MCP tool call blocked — redaction failed.", http.StatusForbidden)
						return
					}
					params["arguments"] = mutated
					mutatedBody, marshalErr := json.Marshal(raw)
					if marshalErr != nil {
						slog.Warn("failed to marshal redacted MCP body, denying (fail-closed)",
							"server", serverName, "error", marshalErr)
						p.logPolicy(r, scope, "mcp.tool_call", "redaction-error", "Failed to redact request")
						http.Error(w, "Moat: MCP tool call blocked — redaction failed.", http.StatusForbidden)
						return
					}
					bodyBytes = mutatedBody
					r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
					r.ContentLength = int64(len(bodyBytes))
				}
			}
		}
	}

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
			credValue = findCredByGrant(rc.Credentials, mcpServer.Auth.Grant)
		}

		// Fall back to credential store (legacy single-run mode).
		if credValue == "" && credStore != nil {
			cred, credErr := credStore.Get(credential.Provider(mcpServer.Auth.Grant))
			if credErr == nil {
				credValue = cred.Token
			}
		}

		if credValue == "" {
			http.Error(w, fmt.Sprintf("MOAT: Failed to load credential for '%s'. Grant: %s. Run: moat grant %s",
				serverName, mcpServer.Auth.Grant, grantToCommand(mcpServer.Auth.Grant)),
				http.StatusInternalServerError)
			return
		}

		// Inject the real credential
		proxyReq.Header.Set(mcpServer.Auth.Header, formatCredValue(mcpServer.Auth.Grant, credValue))
	}

	slog.Debug("MCP relay forwarding", "server", serverName, "method", proxyReq.Method, "url", targetURL.String())

	// Send request to actual MCP server using the reused client
	resp, err := mcpRelayClient.Do(proxyReq)
	if err != nil {
		http.Error(w, fmt.Sprintf("MOAT: Failed to connect to MCP server '%s' at %s: %v",
			serverName, targetURL.String(), err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	slog.Debug("MCP relay response", "server", serverName, "status", resp.StatusCode)

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
