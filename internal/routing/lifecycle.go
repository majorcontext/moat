package routing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/andybons/moat/internal/proxy"
)

// Lifecycle manages the shared reverse proxy lifecycle.
type Lifecycle struct {
	dir     string
	port    int
	server  *ProxyServer
	routes  *RouteTable
	isOwner bool      // true if this instance started the proxy
	ca      *proxy.CA // CA for TLS (nil if TLS disabled)
}

// NewLifecycle creates a lifecycle manager for the proxy.
// If desiredPort is 0, a random available port will be used.
// If desiredPort is negative, the default port (8080) will be used.
func NewLifecycle(dir string, desiredPort int) (*Lifecycle, error) {
	routes, err := NewRouteTable(dir)
	if err != nil {
		return nil, err
	}

	if desiredPort < 0 {
		desiredPort = 8080
	}

	return &Lifecycle{
		dir:    dir,
		port:   desiredPort,
		routes: routes,
	}, nil
}

// EnableTLS enables TLS support by loading or creating a CA.
// Must be called before EnsureRunning().
// Returns true if a new CA was created (caller should print trust instructions).
func (lc *Lifecycle) EnableTLS() (newCA bool, err error) {
	caDir := filepath.Join(lc.dir, "ca")

	// Check if CA already exists
	certPath := filepath.Join(caDir, "ca.crt")
	_, statErr := os.Stat(certPath)
	newCA = os.IsNotExist(statErr)

	ca, err := proxy.NewCA(caDir)
	if err != nil {
		return false, fmt.Errorf("loading/creating CA: %w", err)
	}

	lc.ca = ca
	return newCA, nil
}

// CA returns the CA used for TLS, or nil if TLS is not enabled.
func (lc *Lifecycle) CA() *proxy.CA {
	return lc.ca
}

// EnsureRunning starts the proxy if not already running.
func (lc *Lifecycle) EnsureRunning() error {
	// Check for existing proxy
	lock, err := LoadProxyLock(lc.dir)
	if err != nil {
		return fmt.Errorf("loading proxy lock: %w", err)
	}

	if lock != nil && lock.IsAlive() {
		// Proxy already running - check for port mismatch only if specific port requested
		if lc.port > 0 && lock.Port != lc.port {
			return fmt.Errorf("proxy port mismatch: running on %d, requested %d. Either unset MOAT_PROXY_PORT, or stop all agents to restart the proxy", lock.Port, lc.port)
		}
		lc.port = lock.Port
		lc.isOwner = false
		return nil
	}

	// Clean up stale lock if exists
	if lock != nil {
		_ = RemoveProxyLock(lc.dir)
	}

	// Start new proxy
	lc.server = NewProxyServer(lc.routes)

	// Enable TLS if CA is configured
	if lc.ca != nil {
		if err := lc.server.EnableTLS(lc.ca); err != nil {
			return fmt.Errorf("enabling TLS: %w", err)
		}
	}

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
		_ = lc.server.Stop(context.Background())
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
