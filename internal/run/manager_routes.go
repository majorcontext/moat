package run

// This file owns the manager's side of the routing table: registering a run's
// published ports when its container starts, and rebuilding the shared table
// when this process takes ownership of the routing proxy.

import (
	"context"
	"fmt"
	"time"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/storage"
	"github.com/majorcontext/moat/internal/ui"
)

// reconcileTimeout bounds a single container query during reconciliation, so a
// wedged container runtime delays `moat run` by seconds rather than hanging it.
const reconcileTimeout = 5 * time.Second

// setupPortBindings retrieves the host-side port mappings for a container's
// exposed ports and registers them as routes with both the local route table
// and the proxy daemon. Port binding lookup is retried because the container
// runtime may not have mappings ready immediately after start.
func (m *Manager) setupPortBindings(ctx context.Context, r *Run) {
	if len(r.Ports) == 0 {
		return
	}

	var bindings map[int]int
	var err error
	for i := 0; i < 5; i++ {
		bindings, err = m.defaultRuntime().GetPortBindings(ctx, r.ContainerID)
		if err != nil || len(bindings) >= len(r.Ports) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		ui.Warnf("Getting port bindings: %v", err)
		return
	}

	hostPorts, services := serviceEndpoints(r.Ports, bindings)
	r.HostPorts = hostPorts
	if len(services) > 0 {
		if err := m.routes.Add(r.Name, services); err != nil {
			ui.Warnf("Registering routes: %v", err)
		}
		// Snapshot daemonClient under lock to avoid racing with Create()
		m.mu.RLock()
		dc := m.daemonClient
		m.mu.RUnlock()
		if dc != nil {
			if err := dc.RegisterRoutes(ctx, r.Name, services); err != nil {
				log.Debug("failed to register routes via daemon", "error", err)
			}
		}
	}
}

// serviceEndpoints maps a run's service name -> container port declarations
// onto the ports the runtime actually published, returning both the host port
// per service and the loopback address the routing proxy forwards to. Container
// ports with no published binding are skipped.
func serviceEndpoints(ports map[string]int, bindings map[int]int) (map[string]int, map[string]string) {
	hostPorts := make(map[string]int, len(ports))
	services := make(map[string]string, len(ports))
	for serviceName, containerPort := range ports {
		hostPort, ok := bindings[containerPort]
		if !ok {
			continue
		}
		hostPorts[serviceName] = hostPort
		services[serviceName] = fmt.Sprintf("127.0.0.1:%d", hostPort)
	}
	return hostPorts, services
}

// reconcileRoutes rebuilds the shared route table from the containers that are
// actually running, then drops entries nothing is serving.
//
// Route registration is otherwise one-shot at container start, so a run whose
// entry is lost — by an older moat version's delete-on-empty, a hand-edited
// file, or a crash mid-write — stays invisible to the discovery index and
// `moat open` for the life of its container (issue #451). Taking ownership of
// the routing proxy is the natural repair point: the proxy was not running, so
// there is no in-flight traffic to disturb, and the table's contents are about
// to become load-bearing again.
//
// Both halves are best-effort. A run whose runtime is unavailable (a run
// created under a different container runtime) or whose port bindings cannot
// be read is left exactly as it is in the table.
//
// Candidates come from on-disk run metadata rather than the manager's in-memory
// map, which is a snapshot taken at construction — by the time Create reaches
// the proxy, an image pull may have made it minutes old, and runs another moat
// process started in the meantime would be invisible.
func (m *Manager) reconcileRoutes(ctx context.Context) {
	// Agents are shielded from the prune sweep unless their container is
	// positively confirmed to be gone. A running container whose route could
	// not be rebuilt still deserves protection — the server inside it may not
	// be listening yet — and so does a container whose state could not be read
	// at all, matching how loadPersistedRuns refuses to act on an unconfirmed
	// check.
	live := make(map[string]struct{})

	for _, meta := range portExposingRuns() {
		rt, err := m.runtimePool.Get(container.RuntimeType(meta.Runtime))
		if err != nil {
			log.Debug("reconcile routes: runtime unavailable", "agent", meta.Name, "runtime", meta.Runtime, "error", err)
			live[meta.Name] = struct{}{}
			continue
		}

		stateCtx, cancel := context.WithTimeout(ctx, reconcileTimeout)
		state, err := rt.ContainerState(stateCtx, meta.ContainerID)
		cancel()
		if err != nil {
			log.Debug("reconcile routes: container state check failed", "agent", meta.Name, "error", err)
			live[meta.Name] = struct{}{}
			continue
		}
		if state != "running" {
			continue
		}
		live[meta.Name] = struct{}{}

		bindCtx, cancel := context.WithTimeout(ctx, reconcileTimeout)
		bindings, err := rt.GetPortBindings(bindCtx, meta.ContainerID)
		cancel()
		if err != nil {
			log.Debug("reconcile routes: port binding lookup failed", "agent", meta.Name, "error", err)
			continue
		}

		_, services := serviceEndpoints(meta.Ports, bindings)
		if len(services) == 0 {
			continue
		}
		// Written straight to the shared route table rather than via the daemon:
		// the daemon's RouteTable is backed by the same routes.json in
		// ~/.moat/proxy, so a daemon round-trip would write the identical entry.
		if err := m.routes.Add(meta.Name, services); err != nil {
			log.Debug("reconcile routes: registering route failed", "agent", meta.Name, "error", err)
			continue
		}
		log.Debug("reconcile routes: re-registered route", "agent", meta.Name, "services", services)
	}

	if pruned := m.routes.PruneUnreachable(live); len(pruned) > 0 {
		log.Debug("reconcile routes: pruned unreachable routes", "agents", pruned)
	}
}

// portExposingRuns reads run metadata from disk and returns the runs that could
// hold a route: named, port-exposing, with a container, and not persisted in a
// terminal state. Whether the container is actually up is settled by the
// runtime, not by this (possibly stale) persisted state.
func portExposingRuns() []storage.Metadata {
	baseDir := storage.DefaultBaseDir()
	runIDs, err := storage.ListRunDirs(baseDir)
	if err != nil {
		log.Debug("reconcile routes: listing run directories failed", "error", err)
		return nil
	}

	var out []storage.Metadata
	for _, runID := range runIDs {
		store, err := storage.NewRunStore(baseDir, runID)
		if err != nil {
			log.Debug("reconcile routes: opening run store failed", "run", runID, "error", err)
			continue
		}
		meta, err := store.LoadMetadata()
		if err != nil {
			log.Debug("reconcile routes: loading run metadata failed", "run", runID, "error", err)
			continue
		}
		if meta.Name == "" || meta.ContainerID == "" || len(meta.Ports) == 0 {
			continue
		}
		if meta.State == string(StateStopped) || meta.State == string(StateFailed) {
			continue
		}
		out = append(out, meta)
	}
	return out
}
