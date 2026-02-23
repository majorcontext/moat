package proxy

import (
	"context"
	"fmt"
	"net/url"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/log"
)

// ChainProxy represents a running proxy sidecar in the chain.
type ChainProxy struct {
	Name        string
	ContainerID string
	Host        string // hostname or IP reachable from the network
	Port        int    // listen port inside the container
}

// Chain manages an ordered list of proxy sidecars.
// Traffic flows: container -> proxy[0] -> proxy[1] -> ... -> moat proxy -> internet.
type Chain struct {
	proxies []ChainProxy
}

// StartChain starts proxy sidecar containers in order, wiring each to forward
// to the next proxy in the chain. The last proxy forwards to moatProxyAddr,
// which is Moat's credential-injecting proxy.
//
// Each proxy receives HTTP_PROXY/HTTPS_PROXY pointing to its upstream (the next
// proxy in the chain, or the Moat proxy for the last entry).
func StartChain(
	ctx context.Context,
	entries []config.ProxyChainEntry,
	svcMgr container.ServiceManager,
	runID string,
	moatProxyAddr string,
) (*Chain, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	chain := &Chain{
		proxies: make([]ChainProxy, 0, len(entries)),
	}

	// Start proxies in order. Each one's upstream is the next in the chain.
	// The last proxy's upstream is the Moat credential proxy.
	for i, entry := range entries {
		// Determine upstream URL
		var upstreamURL string
		if i < len(entries)-1 {
			// Upstream is the next proxy in chain (by container name on the Docker network)
			next := entries[i+1]
			upstreamURL = fmt.Sprintf("http://moat-proxy-%s-%s:%d", runID, next.Name, next.Port)
		} else {
			// Last proxy in chain -> upstream is Moat's credential proxy
			upstreamURL = "http://" + moatProxyAddr
		}

		// Build environment: pass upstream proxy info + user env
		env := make(map[string]string)
		for k, v := range entry.Env {
			env[k] = v
		}
		env["HTTP_PROXY"] = upstreamURL
		env["HTTPS_PROXY"] = upstreamURL
		env["http_proxy"] = upstreamURL
		env["https_proxy"] = upstreamURL

		containerName := fmt.Sprintf("moat-proxy-%s-%s", runID, entry.Name)

		svcCfg := container.ServiceConfig{
			Name:    containerName,
			Image:   entry.Image,
			Env:     env,
			RunID:   runID,
			Version: "latest",
		}

		info, err := svcMgr.StartService(ctx, svcCfg)
		if err != nil {
			// Cleanup already-started proxies
			chain.Stop(ctx, svcMgr)
			return nil, fmt.Errorf("starting proxy %q: %w", entry.Name, err)
		}

		cp := ChainProxy{
			Name:        entry.Name,
			ContainerID: info.ID,
			Host:        containerName, // use container name as hostname on Docker network
			Port:        entry.Port,
		}
		chain.proxies = append(chain.proxies, cp)

		log.Debug("chain proxy started",
			"name", entry.Name,
			"container_id", info.ID,
			"image", entry.Image,
			"upstream", upstreamURL)
	}

	// Sidecar containers communicate via the Docker network using container
	// names as hostnames. These names only resolve inside the network, so we
	// cannot dial them from the host. Docker networking handles container
	// readiness — the first connection attempt from the main container will
	// retry via the kernel TCP backlog until the proxy is listening.

	return chain, nil
}

// EntryAddr returns the address (host:port) of the first proxy in the chain.
// The main container's HTTP_PROXY should point here.
// Returns empty string if the chain is empty.
func (c *Chain) EntryAddr() string {
	if c == nil || len(c.proxies) == 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d", c.proxies[0].Host, c.proxies[0].Port)
}

// ContainerIDs returns all sidecar container IDs for metadata storage.
func (c *Chain) ContainerIDs() map[string]string {
	if c == nil {
		return nil
	}
	ids := make(map[string]string, len(c.proxies))
	for _, p := range c.proxies {
		ids[p.Name] = p.ContainerID
	}
	return ids
}

// Names returns the names of all proxies in the chain, in order.
func (c *Chain) Names() []string {
	if c == nil {
		return nil
	}
	names := make([]string, len(c.proxies))
	for i, p := range c.proxies {
		names[i] = p.Name
	}
	return names
}

// Stop terminates all proxy sidecar containers.
func (c *Chain) Stop(ctx context.Context, svcMgr container.ServiceManager) {
	if c == nil || svcMgr == nil {
		return
	}
	// Stop in reverse order
	for i := len(c.proxies) - 1; i >= 0; i-- {
		p := c.proxies[i]
		log.Debug("stopping chain proxy", "name", p.Name, "container_id", p.ContainerID)
		if err := svcMgr.StopService(ctx, container.ServiceInfo{ID: p.ContainerID}); err != nil {
			log.Debug("failed to stop chain proxy", "name", p.Name, "error", err)
		}
	}
}

// ChainEntryURL returns the proxy URL for the first proxy in the chain.
// Returns nil if the chain is empty.
func (c *Chain) ChainEntryURL() *url.URL {
	if c == nil || len(c.proxies) == 0 {
		return nil
	}
	return &url.URL{
		Scheme: "http",
		Host:   c.EntryAddr(),
	}
}
