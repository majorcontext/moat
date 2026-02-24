package proxy

import (
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/majorcontext/moat/internal/log"
)

// relayClient is a reused HTTP client for relay requests. It bypasses proxy
// settings to prevent circular proxy loops — the proxy runs on the host,
// where localhost correctly reaches host-side services.
var relayClient = &http.Client{
	Transport: &http.Transport{
		Proxy: nil, // Disable proxy - connect directly to target
	},
}

// AddRelay registers a named relay endpoint. Requests to /relay/{name}/{path...}
// are forwarded to the target URL with credential injection. This is used when
// the target host would be in NO_PROXY (e.g., a host-side proxy reachable via
// the same address as the Moat proxy), which would cause direct connections to
// bypass credential injection.
func (p *Proxy) AddRelay(name, targetURL string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.relays == nil {
		p.relays = make(map[string]string)
	}
	p.relays[name] = targetURL
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

	// Inject credentials and extra headers for the target host
	p.mu.RLock()
	if cred, ok := p.credentials[targetURL.Host]; ok {
		proxyReq.Header.Set(cred.Name, cred.Value)
		log.Debug("credential injected",
			"subsystem", "proxy",
			"action", "inject",
			"grant", cred.Grant,
			"host", targetURL.Host,
			"header", cred.Name,
			"method", r.Method,
			"path", rest)
	}
	if extras, ok := p.extraHeaders[targetURL.Host]; ok {
		for _, h := range extras {
			if existing := proxyReq.Header.Get(h.Name); existing != "" {
				proxyReq.Header.Set(h.Name, existing+","+h.Value)
			} else {
				proxyReq.Header.Set(h.Name, h.Value)
			}
		}
	}
	p.mu.RUnlock()

	// Forward to target
	resp, err := relayClient.Do(proxyReq)
	if err != nil {
		log.Debug("relay failed",
			"subsystem", "proxy",
			"action", "relay-error",
			"relay", name,
			"target", targetURL.String(),
			"error", err)
		http.Error(w, "MOAT: Failed to connect to "+name+" relay at "+targetURL.Host+": "+err.Error(),
			http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Copy status code and body with streaming support
	w.WriteHeader(resp.StatusCode)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	_, _ = io.Copy(w, resp.Body)
}
