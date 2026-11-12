package routing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/majorcontext/gatekeeper/proxy"
)

// Lifecycle manages the shared reverse proxy lifecycle.
type Lifecycle struct {
	mu        sync.Mutex
	dir       string
	port      int
	server    *ProxyServer
	routes    *RouteTable
	startedAt time.Time // identifies this instance even within the same process
	isOwner   bool      // true if this instance started the proxy
	ca        *proxy.CA // CA for TLS (nil if TLS disabled)
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
	lc.mu.Lock()
	defer lc.mu.Unlock()
	caDir := filepath.Join(lc.dir, "ca")

	// Check if CA already exists
	certPath := filepath.Join(caDir, "ca.crt")
	_, statErr := os.Stat(certPath)
	newCA = os.IsNotExist(statErr)

	ca, err := proxy.NewCAWithSubject(caDir, "Moat", "Moat CA")
	if err != nil {
		return false, fmt.Errorf("loading/creating CA: %w", err)
	}

	lc.ca = ca
	return newCA, nil
}

// CA returns the CA used for TLS, or nil if TLS is not enabled.
func (lc *Lifecycle) CA() *proxy.CA {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return lc.ca
}

// EnsureRunning starts the proxy if not already running.
func (lc *Lifecycle) EnsureRunning() error {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	unlock, err := lc.lockLifecycle()
	if err != nil {
		return err
	}
	defer unlock()
	// Check for existing proxy
	lock, err := LoadProxyLock(lc.dir)
	if err != nil {
		return fmt.Errorf("loading proxy lock: %w", err)
	}

	// A live PID does not imply its listener is still serving.
	if lock != nil && lock.IsAlive() && proxyListening(lock.Port) {
		// Proxy already running - check for port mismatch only if specific port requested
		if lc.port > 0 && lock.Port != lc.port {
			return fmt.Errorf("proxy port mismatch: running on %d, requested %d. Either unset MOAT_PROXY_PORT, or stop all agents to restart the proxy", lock.Port, lc.port)
		}
		lc.port = lock.Port
		lc.isOwner = lc.isOwner && lc.server != nil && lock.PID == os.Getpid() && lc.server.Port() == lock.Port && lc.startedAt.Equal(lock.StartedAt)
		return nil
	}

	// Clean up stale lock if exists
	if lock != nil {
		_ = RemoveProxyLock(lc.dir)
	}

	// Release any failed owned listener before replacing the server.
	if lc.server != nil && lc.isOwner {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = lc.server.Stop(stopCtx)
		cancel()
	}
	lc.isOwner = false
	lc.server = nil

	// Start new proxy
	lc.server = NewProxyServer(lc.routes)

	// Enable TLS if CA is configured
	if lc.ca != nil {
		if err := lc.server.EnableTLS(lc.ca); err != nil {
			return fmt.Errorf("enabling TLS: %w", err)
		}
	}

	if err := lc.server.Start(lc.port); err != nil {
		// The routing port is intentionally deterministic (no fallback to a
		// random port — advertised URLs must stay stable). If the chosen port
		// is taken, point the user at how to change it rather than surfacing a
		// raw bind error.
		if errors.Is(err, syscall.EADDRINUSE) {
			// Don't wrap the raw "bind: address already in use" — the whole
			// point is to replace that noise with an actionable message.
			return fmt.Errorf("routing proxy port %d is already in use — choose another with MOAT_PROXY_PORT=<port> or set proxy.port in ~/.moat/config.yaml", lc.port)
		}
		return fmt.Errorf("starting proxy: %w", err)
	}

	lc.port = lc.server.Port()

	// Save lock file
	lc.startedAt = time.Now()
	if err := SaveProxyLock(lc.dir, ProxyLockInfo{
		PID:       os.Getpid(),
		Port:      lc.port,
		StartedAt: lc.startedAt,
	}); err != nil {
		_ = lc.server.Stop(context.Background())
		return fmt.Errorf("saving proxy lock: %w", err)
	}

	lc.isOwner = true
	return nil
}

// Port returns the port the proxy is running on.
func (lc *Lifecycle) Port() int {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return lc.port
}

// IsOwner reports whether this process started the routing proxy currently
// serving the port, as opposed to attaching to one another process owns.
// Callers use this to run work that must happen exactly once per proxy
// lifetime, such as reconciling the route table against running containers.
func (lc *Lifecycle) IsOwner() bool {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return lc.isOwner
}

// Routes returns the route table.
func (lc *Lifecycle) Routes() *RouteTable {
	return lc.routes
}

// Stop stops the proxy if this instance owns it.
func (lc *Lifecycle) Stop(ctx context.Context) error {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	unlock, err := lc.lockLifecycle()
	if err != nil {
		return err
	}
	defer unlock()
	if !lc.isOwner || lc.server == nil {
		return nil
	}

	if err = lc.server.Stop(ctx); err != nil {
		return err
	}

	lc.isOwner = false
	lc.server = nil
	// Another process may have recovered the listener while we were stopping.
	lock, err := LoadProxyLock(lc.dir)
	if err != nil {
		return err
	}
	if lock != nil && lock.PID == os.Getpid() && lock.Port == lc.port && lc.startedAt.Equal(lock.StartedAt) {
		return RemoveProxyLock(lc.dir)
	}
	return nil
}

// ShouldStop returns true if there are no more registered agents.
func (lc *Lifecycle) ShouldStop() bool {
	return len(lc.routes.Agents()) == 0
}

// proxyListening is deliberately bounded; recovery must not stall run monitors.
func proxyListening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// lockLifecycle serializes listener/lock-file transitions across managers and
// processes. It must not use routes.lock: shutdown can wait for handlers that
// themselves need the route table.
func (lc *Lifecycle) lockLifecycle() (func(), error) {
	f, err := os.OpenFile(filepath.Join(lc.dir, "lifecycle.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	unlock, err := lockFile(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { unlock(); _ = f.Close() }, nil
}
