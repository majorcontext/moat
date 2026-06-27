package run

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/daemon"
	"github.com/majorcontext/moat/internal/hostnames"
	"github.com/majorcontext/moat/internal/id"
	"github.com/majorcontext/moat/internal/log"
	_ "github.com/majorcontext/moat/internal/providers" // register all credential providers
	"github.com/majorcontext/moat/internal/routing"
	"github.com/majorcontext/moat/internal/storage"
)

// Timing constants for run lifecycle operations.
const (
	// containerStartDelay is how long to wait after StartAttached begins before
	// updating run state to "running". This delay ensures the container process
	// has started and the TTY is attached before we report it as running.
	// The value is chosen to be long enough for the attach to establish but
	// short enough to not noticeably delay state updates.
	containerStartDelay = 100 * time.Millisecond
)

// Aliases to the shared hostnames package so existing callsites in this file
// keep their current names. Canonical definitions live in internal/hostnames
// so that the daemon, proxy, and manager all agree on the exact strings.
const (
	syntheticProxyHost   = hostnames.Proxy
	syntheticHostGateway = hostnames.HostGateway
)

// Manager handles run lifecycle operations.
type Manager struct {
	runtimePool    *container.RuntimePool
	runtimeType    string // Cached at init; safe to read after Close()
	runs           map[string]*Run
	routes         *routing.RouteTable
	proxyLifecycle *routing.Lifecycle
	daemonClient   *daemon.Client
	mu             sync.RWMutex

	// ctx/cancel for general manager lifecycle.
	ctx    context.Context
	cancel context.CancelFunc

	// monitorCtx/monitorCancel control monitorContainerExit goroutines.
	// Separate from the main ctx so monitors can outlive general operations
	// but still be canceled by Close(). Close() cancels monitorCtx first,
	// then waits on monitorWg with a bounded timeout to prevent deadlocks
	// when WaitContainer blocks (e.g., Docker daemon slow on custom networks).
	monitorCtx    context.Context
	monitorCancel context.CancelFunc

	// monitorWg tracks active monitorContainerExit goroutines.
	// Close() waits on this (with a timeout) after canceling monitorCtx.
	monitorWg sync.WaitGroup
}

// runtimeForRun returns the correct container runtime for an existing run.
// It uses the run's Runtime field to look up the matching runtime from the pool.
// For legacy runs without a Runtime field, falls back to the default runtime.
func (m *Manager) runtimeForRun(r *Run) (container.Runtime, error) {
	return m.runtimePool.Get(container.RuntimeType(r.Runtime))
}

// defaultRuntime returns the default runtime for new run creation.
// This is only called during Create/Start/StartAttached flows where the pool
// is guaranteed to be open. Panics if the pool is closed, indicating a
// programming error (these methods must not be called after Close).
func (m *Manager) defaultRuntime() container.Runtime {
	rt, err := m.runtimePool.Default()
	if err != nil {
		panic("bug: runtime pool closed during active operation: " + err.Error())
	}
	return rt
}

// ManagerOptions configures the run manager.
type ManagerOptions struct {
	// NoSandbox disables gVisor sandbox for Docker containers.
	// If nil, uses platform-aware defaults (gVisor on Linux, standard on macOS/Windows).
	NoSandbox *bool

	// ReapOrphanNetworks enables a one-shot sweep of moat-managed networks whose
	// run directories no longer exist. Set by commands that create networks
	// (`moat run`) or explicitly clean up (`moat clean`). Read-only commands
	// leave it false to avoid the per-invocation cost of listing networks.
	ReapOrphanNetworks bool
}

// NewManagerWithOptions creates a new run manager with the given options.
func NewManagerWithOptions(opts ManagerOptions) (*Manager, error) {
	var runtimeOpts container.RuntimeOptions
	if opts.NoSandbox != nil {
		// User explicitly set --no-sandbox flag
		runtimeOpts.Sandbox = !*opts.NoSandbox
	} else {
		// Use platform-aware defaults
		runtimeOpts = container.DefaultRuntimeOptions()
	}

	pool, err := container.NewRuntimePool(runtimeOpts)
	if err != nil {
		return nil, fmt.Errorf("initializing container runtime: %w", err)
	}

	proxyDir := filepath.Join(config.GlobalConfigDir(), "proxy")

	globalCfg, _ := config.LoadGlobal()
	proxyPort := globalCfg.Proxy.Port

	lifecycle, err := routing.NewLifecycle(proxyDir, proxyPort)
	if err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("initializing proxy lifecycle: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	monitorCtx, monitorCancel := context.WithCancel(context.Background())
	defaultRT, _ := pool.Default()
	m := &Manager{
		runtimePool:    pool,
		runtimeType:    string(defaultRT.Type()),
		runs:           make(map[string]*Run),
		routes:         lifecycle.Routes(),
		proxyLifecycle: lifecycle,
		ctx:            ctx,
		cancel:         cancel,
		monitorCtx:     monitorCtx,
		monitorCancel:  monitorCancel,
	}

	// Load existing runs from disk and reconcile with container state.
	// Use a 30-second timeout so stale runs can't block CLI startup.
	loadCtx, loadCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer loadCancel()
	if err := m.loadPersistedRuns(loadCtx); err != nil {
		log.Debug("loading persisted runs", "error", err)
		// Non-fatal - continue with empty runs map
	}

	// Reap orphan moat networks left behind by killed/crashed runs. Each leaked
	// network on Apple's container runtime keeps a vmnet daemon alive and
	// consumes a /24 from the IP pool; eventually `container network create`
	// hangs. See https://github.com/majorcontext/moat/issues/315.
	//
	// Only sweep when explicitly requested (commands that create networks or
	// clean up). Read-only commands skip it to avoid the per-invocation cost.
	// Long-term this belongs in the daemon — see
	// https://github.com/majorcontext/moat/issues/341.
	if opts.ReapOrphanNetworks {
		reapCtx, reapCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer reapCancel()
		m.cleanOrphanNetworks(reapCtx)
	}

	return m, nil
}

// cleanOrphanNetworks removes moat-managed networks whose run directory has
// been deleted from disk. Best-effort: errors are logged but do not fail
// manager initialization. Only sweeps the default runtime to avoid eagerly
// initializing other runtimes.
//
// Networks are listed BEFORE run dirs to close the race with concurrent
// Create() calls in another process: Create() makes the run dir before any
// network op, so if a network exists at list time but its dir wasn't seen at
// the later snapshot, the dir genuinely doesn't exist.
//
// Networks whose suffix isn't a valid run ID are skipped — protects users
// who happen to have a network named e.g. "moat-shared".
func (m *Manager) cleanOrphanNetworks(ctx context.Context) {
	rt, err := m.runtimePool.Default()
	if err != nil {
		return
	}
	netMgr := rt.NetworkManager()
	if netMgr == nil {
		return
	}

	networks, err := netMgr.ListNetworks(ctx)
	if err != nil {
		log.Debug("orphan sweep: listing networks failed", "error", err)
		return
	}

	known, err := storage.ListRunDirNames(storage.DefaultBaseDir())
	if err != nil {
		log.Debug("orphan sweep: scanning run dirs failed", "error", err)
		return
	}

	var reaped int
	for _, n := range networks {
		runID, ok := strings.CutPrefix(n.Name, "moat-")
		if !ok {
			continue
		}
		if !id.IsValid(runID, "run") {
			continue
		}
		if _, alive := known[runID]; alive {
			continue
		}
		// Bound each removal individually so one wedged network doesn't burn
		// the whole sweep budget.
		rmCtx, rmCancel := context.WithTimeout(ctx, 5*time.Second)
		log.Debug("removing orphan network", "name", n.Name, "id", n.ID)
		if rmErr := netMgr.ForceRemoveNetwork(rmCtx, n.ID); rmErr != nil {
			log.Debug("orphan sweep: failed to remove network", "name", n.Name, "error", rmErr)
			rmCancel()
			continue
		}
		rmCancel()
		reaped++
	}
	if reaped > 0 {
		log.Info("reaped orphan moat networks", "count", reaped)
	}
}

// NewManager creates a new run manager with default options.
func NewManager() (*Manager, error) {
	return NewManagerWithOptions(ManagerOptions{})
}

// Get retrieves a run by ID.
func (m *Manager) Get(runID string) (*Run, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	r, ok := m.runs[runID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	return r, nil
}

// List returns all runs.
func (m *Manager) List() []*Run {
	m.mu.RLock()
	defer m.mu.RUnlock()

	runs := make([]*Run, 0, len(m.runs))
	for _, r := range m.runs {
		runs = append(runs, r)
	}
	return runs
}

// RuntimeType returns the container runtime type (docker or apple).
// Uses a value cached at init, so it is safe to call after Close().
func (m *Manager) RuntimeType() string {
	return m.runtimeType
}

// RuntimePool returns the manager's runtime pool. CLI commands that need
// to query resources across runtimes (e.g., images, containers) should use
// this instead of creating a separate pool.
func (m *Manager) RuntimePool() *container.RuntimePool {
	return m.runtimePool
}

// RoutingPort returns the port the shared routing proxy is bound to. This is
// the actual listening port (which may differ from the configured default if
// it was taken and the proxy fell back to an OS-assigned port), so it's the
// truthful value to advertise in user-facing URLs.
func (m *Manager) RoutingPort() int {
	return m.proxyLifecycle.Port()
}

// Close releases manager resources.
// It cancels monitor goroutines and waits (with a bounded timeout) for them
// to finish capturing logs and updating state before closing the runtime.
func (m *Manager) Close() error {
	// Cancel the manager context.
	if m.cancel != nil {
		m.cancel()
	}

	// Cancel monitor goroutines so WaitContainer unblocks. This prevents
	// Close() from deadlocking when the Docker daemon is slow to report
	// container exit (e.g., on custom networks with service dependencies).
	// See https://github.com/majorcontext/moat/issues/315
	if m.monitorCancel != nil {
		m.monitorCancel()
	}

	// Wait for monitor goroutines to finish with a bounded timeout.
	// Normally monitors complete quickly after context cancellation.
	// The timeout is a safety net for cases where even a canceled
	// WaitContainer doesn't return (e.g., Docker daemon unresponsive).
	monitorDone := make(chan struct{})
	go func() {
		m.monitorWg.Wait()
		close(monitorDone)
	}()
	select {
	case <-monitorDone:
		// All monitors finished cleanly.
	case <-time.After(10 * time.Second):
		log.Warn("timed out waiting for container monitors to finish; proceeding with shutdown")
	}

	// Stop all proxy/SSH servers and unregister runs from daemon,
	// with a 10-second overall timeout.
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer closeCancel()

	m.mu.RLock()
	for _, r := range m.runs {
		if err := r.stopProxyServer(closeCtx); err != nil {
			log.Debug("failed to stop proxy during manager close", "run", r.ID, "error", err)
		}
		if r.ProxyAuthToken != "" && m.daemonClient != nil {
			if err := m.daemonClient.UnregisterRun(closeCtx, r.ProxyAuthToken); err != nil {
				log.Debug("failed to unregister run from daemon during manager close", "run", r.ID, "error", err)
			}
		}
		if err := r.stopSSHAgentServer(); err != nil {
			log.Debug("failed to stop SSH agent during manager close", "run", r.ID, "error", err)
		}
	}
	m.mu.RUnlock()

	return m.runtimePool.Close()
}
