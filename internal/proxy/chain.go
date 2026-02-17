// Package proxy provides a TLS-intercepting HTTP proxy for credential injection.
// This file implements proxy chain management for composing multiple upstream proxies.
package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/log"
)

// ChainEntry represents a running proxy in the chain.
type ChainEntry struct {
	Name string
	URL  *url.URL // The proxy URL to connect to
	cmd  *exec.Cmd
}

// Chain manages an ordered list of upstream proxies.
// Moat's proxy forwards through proxy[0], which forwards through proxy[1], etc.
// Only the first proxy in the chain is directly used as the upstream for Moat's proxy.
// Subsequent proxies are chained by configuring each proxy's HTTP_PROXY environment
// variable to point to the next proxy in the chain.
type Chain struct {
	entries []ChainEntry
	mu      sync.Mutex
}

// NewChain creates a proxy chain from configuration entries.
// Managed proxies (those with Command) are started in order.
// Each managed proxy receives the next proxy's URL as its HTTP_PROXY/HTTPS_PROXY.
func NewChain(ctx context.Context, entries []config.ProxyChainEntry) (*Chain, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	chain := &Chain{
		entries: make([]ChainEntry, 0, len(entries)),
	}

	// Resolve all entries, starting managed processes as needed.
	// We process in reverse order so each proxy knows its upstream (the next in the chain).
	resolved := make([]ChainEntry, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		var ce ChainEntry
		ce.Name = entry.Name

		if entry.URL != "" {
			// External proxy -- just parse the URL
			u, err := url.Parse(entry.URL)
			if err != nil {
				chain.Stop()
				return nil, fmt.Errorf("proxies[%d] %q: invalid URL %q: %w", i, entry.Name, entry.URL, err)
			}
			ce.URL = u
		} else {
			// Managed proxy -- start the process
			port, err := findFreePort()
			if err != nil {
				chain.Stop()
				return nil, fmt.Errorf("proxies[%d] %q: finding free port: %w", i, entry.Name, err)
			}

			portEnv := entry.PortEnv
			if portEnv == "" {
				portEnv = "PORT"
			}

			cmd := exec.CommandContext(ctx, entry.Command, entry.Args...) //nolint:gosec
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr

			// Set environment
			cmd.Env = os.Environ()
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%d", portEnv, port))
			for k, v := range entry.Env {
				cmd.Env = append(cmd.Env, k+"="+v)
			}

			// If there's a next proxy in the chain, set it as the upstream
			if i < len(entries)-1 && resolved[i+1].URL != nil {
				nextURL := resolved[i+1].URL.String()
				cmd.Env = append(cmd.Env,
					"HTTP_PROXY="+nextURL,
					"HTTPS_PROXY="+nextURL,
					"http_proxy="+nextURL,
					"https_proxy="+nextURL,
				)
			}

			if err := cmd.Start(); err != nil {
				chain.Stop()
				return nil, fmt.Errorf("proxies[%d] %q: starting process: %w", i, entry.Name, err)
			}

			ce.cmd = cmd
			ce.URL = &url.URL{
				Scheme: "http",
				Host:   fmt.Sprintf("127.0.0.1:%d", port),
			}

			log.Debug("chain proxy started",
				"name", entry.Name,
				"port", port,
				"command", entry.Command)

			// Wait briefly for the proxy to start listening
			if err := waitForPort(port, 5*time.Second); err != nil {
				chain.Stop()
				return nil, fmt.Errorf("proxies[%d] %q: proxy did not start listening on port %d: %w", i, entry.Name, port, err)
			}
		}

		resolved[i] = ce
	}

	chain.entries = resolved
	return chain, nil
}

// UpstreamURL returns the URL of the first proxy in the chain.
// Moat's proxy should forward all outbound requests through this URL.
// Returns nil if the chain is empty.
func (c *Chain) UpstreamURL() *url.URL {
	if c == nil || len(c.entries) == 0 {
		return nil
	}
	return c.entries[0].URL
}

// Names returns the names of all proxies in the chain, in order.
func (c *Chain) Names() []string {
	if c == nil {
		return nil
	}
	names := make([]string, len(c.entries))
	for i, e := range c.entries {
		names[i] = e.Name
	}
	return names
}

// Stop terminates all managed proxy processes.
func (c *Chain) Stop() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.entries {
		if c.entries[i].cmd != nil && c.entries[i].cmd.Process != nil {
			log.Debug("stopping chain proxy", "name", c.entries[i].Name)
			_ = c.entries[i].cmd.Process.Signal(os.Interrupt)

			// Give it a moment to shut down gracefully
			done := make(chan error, 1)
			go func(cmd *exec.Cmd) {
				done <- cmd.Wait()
			}(c.entries[i].cmd)

			select {
			case <-done:
			case <-time.After(3 * time.Second):
				_ = c.entries[i].cmd.Process.Kill()
			}
			c.entries[i].cmd = nil
		}
	}
}

// findFreePort finds a free TCP port on localhost.
func findFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	tcpAddr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		l.Close()
		return 0, fmt.Errorf("unexpected listener address type: %T", l.Addr())
	}
	port := tcpAddr.Port
	l.Close()
	return port, nil
}

// waitForPort waits for a TCP port to accept connections.
func waitForPort(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := "127.0.0.1:" + strconv.Itoa(port)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for port %d", port)
}

// UpstreamTransport creates an http.Transport that routes through the
// first proxy in the chain. Returns nil if no chain is configured.
func (c *Chain) UpstreamTransport() *http.Transport {
	if c == nil || c.UpstreamURL() == nil {
		return nil
	}
	upstreamURL := c.UpstreamURL()
	return &http.Transport{
		Proxy: http.ProxyURL(upstreamURL),
	}
}
