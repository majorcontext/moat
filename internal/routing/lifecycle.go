package routing

import (
	"context"
	"fmt"
	"os"
)

// Lifecycle manages the shared reverse proxy lifecycle.
type Lifecycle struct {
	dir     string
	port    int
	server  *ProxyServer
	routes  *RouteTable
	isOwner bool // true if this instance started the proxy
}

// NewLifecycle creates a lifecycle manager for the proxy.
func NewLifecycle(dir string, desiredPort int) (*Lifecycle, error) {
	routes, err := NewRouteTable(dir)
	if err != nil {
		return nil, err
	}

	if desiredPort == 0 {
		desiredPort = 8080
	}

	return &Lifecycle{
		dir:    dir,
		port:   desiredPort,
		routes: routes,
	}, nil
}

// EnsureRunning starts the proxy if not already running.
func (lc *Lifecycle) EnsureRunning() error {
	// Check for existing proxy
	lock, err := LoadProxyLock(lc.dir)
	if err != nil {
		return fmt.Errorf("loading proxy lock: %w", err)
	}

	if lock != nil && lock.IsAlive() {
		// Proxy already running
		if lc.port != 8080 && lock.Port != lc.port {
			return fmt.Errorf("proxy port mismatch: running on %d, requested %d. Either unset AGENTOPS_PROXY_PORT, or stop all agents to restart the proxy", lock.Port, lc.port)
		}
		lc.port = lock.Port
		lc.isOwner = false
		return nil
	}

	// Clean up stale lock if exists
	if lock != nil {
		RemoveProxyLock(lc.dir)
	}

	// Start new proxy
	lc.server = NewProxyServer(lc.routes)
	if err := lc.server.Start(lc.port); err != nil {
		return fmt.Errorf("starting proxy: %w", err)
	}

	lc.port = lc.server.Port()
	lc.isOwner = true

	// Save lock file
	if err := SaveProxyLock(lc.dir, ProxyLockInfo{
		PID:  os.Getpid(),
		Port: lc.port,
	}); err != nil {
		lc.server.Stop(context.Background())
		return fmt.Errorf("saving proxy lock: %w", err)
	}

	return nil
}

// Port returns the port the proxy is running on.
func (lc *Lifecycle) Port() int {
	return lc.port
}

// Routes returns the route table.
func (lc *Lifecycle) Routes() *RouteTable {
	return lc.routes
}

// Stop stops the proxy if this instance owns it.
func (lc *Lifecycle) Stop(ctx context.Context) error {
	if !lc.isOwner || lc.server == nil {
		return nil
	}

	if err := lc.server.Stop(ctx); err != nil {
		return err
	}

	return RemoveProxyLock(lc.dir)
}

// ShouldStop returns true if there are no more registered agents.
func (lc *Lifecycle) ShouldStop() bool {
	return len(lc.routes.Agents()) == 0
}
