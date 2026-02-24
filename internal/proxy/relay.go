package proxy

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/log"
)

// relayClient is a reused HTTP client for relay requests. It bypasses proxy
// settings to prevent circular proxy loops — the proxy runs on the host,
// where localhost correctly reaches host-side services.
var relayClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 nil, // Disable proxy - connect directly to target
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	},
}

// AddRelay registers a named relay endpoint. Requests to /relay/{name}/{path...}
// are forwarded to the target URL with credential injection. This is used when
// the target host would be in NO_PROXY (e.g., a host-side proxy reachable via
// the same address as the Moat proxy), which would cause direct connections to
// bypass credential injection.
//
// AddRelay must be called before the proxy starts serving.
func (p *Proxy) AddRelay(name, targetURL string) error {
	if name == "" || strings.ContainsAny(name, "/ \t\n\r") {
		return fmt.Errorf("invalid relay name %q: must be non-empty with no slashes or whitespace", name)
	}
	u, err := url.Parse(targetURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("invalid relay target URL %q", targetURL)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.relays == nil {
		p.relays = make(map[string]string)
	}
	p.relays[name] = targetURL
	return nil
}

// handleRelay proxies requests through the Moat proxy to a configured target
// URL with credential injection.
//
// Path format: /relay/{name}/{path...}
// The /relay/{name} prefix is stripped, and the remaining path is appended
// to the configured target URL.
func (p *Proxy) handleRelay(w http.ResponseWriter, r *http.Request) {
	// Extract relay name from path: /relay/anthropic/v1/messages -> anthropic
	relPath := strings.TrimPrefix(r.URL.Path, "/relay/")
	name, rest, _ := strings.Cut(relPath, "/")
	if rest != "" {
		rest = "/" + rest
	}

	// Look up relay target
	p.mu.RLock()
	target, ok := p.relays[name]
	p.mu.RUnlock()

	if !ok {
		http.Error(w, "MOAT: Unknown relay endpoint '"+name+"'", http.StatusNotFound)
		return
	}

	// Build target URL
	targetURL, err := url.Parse(target)
	if err != nil {
		http.Error(w, "MOAT: Invalid relay target URL", http.StatusInternalServerError)
		return
	}
	targetURL.Path = strings.TrimSuffix(targetURL.Path, "/") + rest
	targetURL.RawQuery = r.URL.RawQuery

	// Create forwarded request
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL.String(), r.Body)
	if err != nil {
		http.Error(w, "MOAT: Failed to create relay request", http.StatusInternalServerError)
		return
	}

	// Copy headers (skip proxy-specific ones)
	for key, values := range r.Header {
		if key == "Proxy-Authorization" || key == "Proxy-Connection" {
			continue
		}
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}

	// Inject credentials and extra headers for the target host.
	// Use the helper methods (getCredential, getExtraHeaders, getRemoveHeaders)
	// which handle host:port fallback — credentials are registered by hostname
	// only (isValidHost rejects colons), but targetURL.Host may include a port.
	host := targetURL.Host
	if cred, ok := p.getCredential(host); ok {
		proxyReq.Header.Set(cred.Name, cred.Value)
		log.Debug("credential injected",
			"subsystem", "proxy",
			"action", "inject",
			"grant", cred.Grant,
			"host", host,
			"header", cred.Name,
			"method", r.Method,
			"path", rest)
	}
	mergeExtraHeaders(proxyReq, host, p.getExtraHeaders(host))
	for _, headerName := range p.getRemoveHeaders(host) {
		proxyReq.Header.Del(headerName)
	}

	// Forward to target
	resp, err := relayClient.Do(proxyReq)
	if err != nil {
		log.Debug("relay failed",
			"subsystem", "proxy",
			"action", "relay-error",
			"relay", name,
			"target", targetURL.String(),
			"error", err)
		http.Error(w, "MOAT: Relay '"+name+"' connection failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Copy status code and body with streaming support.
	// Use a flushing copy loop so SSE/chunked streaming responses
	// (e.g., Claude Code's /v1/messages with stream:true) are flushed
	// incrementally rather than buffered in io.Copy's 32KB buffer.
	w.WriteHeader(resp.StatusCode)
	flusher, canFlush := w.(http.Flusher)
	if canFlush {
		flusher.Flush()
	}
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if canFlush {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}
}
