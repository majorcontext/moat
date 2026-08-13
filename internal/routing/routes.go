// Package routing provides hostname-based reverse proxy routing.
package routing

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/majorcontext/moat/internal/log"
)

// dialTimeout bounds the TCP probe used to decide whether a registered
// endpoint is still being served.
const dialTimeout = 500 * time.Millisecond

// pruneWorkers bounds how many endpoints are probed concurrently during a
// prune sweep, so a table full of dead routes doesn't cost
// len(routes) * dialTimeout.
const pruneWorkers = 10

// RouteTable manages agent -> endpoint -> host:port mappings.
//
// The table is backed by routes.json in dir, which is shared by every moat
// process on the host: `moat run` registers a run's published ports, the
// daemon's API writes on behalf of remote registrations, and the routing proxy
// reads it to resolve hostnames. Consequently every mutation is a
// read-modify-write cycle that must be serialized across processes, not just
// across goroutines — see withFileLock.
type RouteTable struct {
	dir    string
	routes map[string]map[string]string // agent -> endpoint -> host:port
	// Every method reloads from disk under the lock before reading, so they all
	// take a write lock — a plain Mutex, not RWMutex, matches the real usage.
	mu sync.Mutex
}

// NewRouteTable creates or loads a route table from the given directory.
func NewRouteTable(dir string) (*RouteTable, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	rt := &RouteTable{
		dir:    dir,
		routes: make(map[string]map[string]string),
	}

	// Warm start from the existing file. Every mutation reloads under the file
	// lock anyway, so a read error here is not fatal.
	if err := rt.reload(); err != nil {
		log.Debug("loading routes at startup", "error", err)
	}

	return rt, nil
}

// Add registers routes for an agent.
func (rt *RouteTable) Add(agent string, endpoints map[string]string) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	return rt.withFileLock(func() error {
		if err := rt.reload(); err != nil { // pick up routes written by other processes
			return err
		}
		rt.routes[agent] = endpoints
		return rt.save()
	})
}

// Remove unregisters an agent's routes.
//
// An empty table is persisted as "{}" rather than deleted. Deleting the file
// used to discard the entries of every still-running agent whenever the map
// transiently emptied, orphaning long-lived runs for the life of their
// container (issue #451).
func (rt *RouteTable) Remove(agent string) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	return rt.withFileLock(func() error {
		if err := rt.reload(); err != nil { // pick up routes written by other processes
			return err
		}
		delete(rt.routes, agent)
		return rt.save()
	})
}

// withFileLock runs fn while holding an exclusive advisory lock on
// <dir>/routes.lock, serializing the reload -> mutate -> save cycle against
// other moat processes. The in-process mutex alone is not enough: without this,
// process B can reload before A's save lands, then write its own copy back and
// silently drop A's entry.
//
// The lock file is created on demand and never removed — unlinking the target
// of an flock would let two processes lock different inodes and reintroduce the
// race.
func (rt *RouteTable) withFileLock(fn func() error) error {
	f, err := os.OpenFile(filepath.Join(rt.dir, "routes.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	unlock, err := lockFile(f)
	if err != nil {
		return err
	}
	defer unlock()

	return fn()
}

// reload replaces the in-memory map with the contents of routes.json.
//
// A missing or unparseable file resets the table to empty: routes.json is a
// derived cache, and a file no consumer can parse must not be preserved. Any
// other read error (permissions, I/O) is returned so mutating callers abort
// instead of saving a map that may no longer reflect what is on disk.
func (rt *RouteTable) reload() error {
	path := filepath.Join(rt.dir, "routes.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			rt.routes = make(map[string]map[string]string)
			return nil
		}
		return err
	}

	var routes map[string]map[string]string
	if err := json.Unmarshal(data, &routes); err != nil || routes == nil {
		if err != nil {
			log.Debug("routes file is unparseable, resetting", "error", err)
		}
		rt.routes = make(map[string]map[string]string)
		return nil
	}
	rt.routes = routes
	return nil
}

// reloadForRead refreshes the in-memory map for a read-only caller. Unlike the
// mutating paths it cannot abort, so a transient read error just leaves the
// previously loaded map in place.
func (rt *RouteTable) reloadForRead() {
	if err := rt.reload(); err != nil {
		log.Debug("reloading routes", "error", err)
	}
}

// Lookup returns the host:port for an agent's endpoint.
// It reloads routes from disk to pick up changes from other processes.
func (rt *RouteTable) Lookup(agent, endpoint string) (string, bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.reloadForRead()

	endpoints, ok := rt.routes[agent]
	if !ok {
		return "", false
	}
	addr, ok := endpoints[endpoint]
	return addr, ok
}

// AgentExists returns true if the agent has registered routes.
// It reloads routes from disk to pick up changes from other processes.
func (rt *RouteTable) AgentExists(agent string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.reloadForRead()

	_, ok := rt.routes[agent]
	return ok
}

// RemoveIfStale checks whether any of the agent's registered endpoints are
// reachable via TCP. If none respond within a short timeout, the route is
// considered stale (leftover from a crashed or stopped process) and is removed.
// Returns true if the route was removed, false if the agent is still alive or
// was not registered.
func (rt *RouteTable) RemoveIfStale(agent string) bool {
	endpoints := rt.Endpoints(agent)
	if endpoints == nil {
		return false
	}
	if anyReachable(endpoints) {
		return false
	}
	return len(rt.removeUnchanged(map[string]map[string]string{agent: endpoints})) == 1
}

// PruneUnreachable removes every agent whose registered endpoints all fail a
// TCP probe, and returns the names removed. Agents named in keep are never
// removed, which lets a caller protect routes it has just verified against the
// container runtime but whose service may not be listening yet.
//
// Reachability, not container enumeration, is the removal test: it cannot
// misfire on runs belonging to another runtime, another credential profile, or
// a hand-added entry that is genuinely serving traffic.
func (rt *RouteTable) PruneUnreachable(keep map[string]struct{}) []string {
	// Probe outside the file lock — dialing while holding it would block every
	// other process's registration for the duration of the sweep.
	snapshot := rt.Snapshot()

	candidates := make(map[string]map[string]string)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, pruneWorkers)

	for agent, endpoints := range snapshot {
		if _, protected := keep[agent]; protected {
			continue
		}
		wg.Add(1)
		go func(agent string, endpoints map[string]string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if anyReachable(endpoints) {
				return
			}
			mu.Lock()
			candidates[agent] = endpoints
			mu.Unlock()
		}(agent, endpoints)
	}
	wg.Wait()

	if len(candidates) == 0 {
		return nil
	}
	return rt.removeUnchanged(candidates)
}

// removeUnchanged deletes the given agents, but only those whose on-disk
// endpoints still match what was probed. An agent that was re-registered while
// the probe was in flight is left alone — its new endpoints were never tested.
// Returns the names actually removed.
func (rt *RouteTable) removeUnchanged(probed map[string]map[string]string) []string {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	var removed []string
	err := rt.withFileLock(func() error {
		if err := rt.reload(); err != nil {
			return err
		}
		for agent, endpoints := range probed {
			current, ok := rt.routes[agent]
			if !ok || !sameEndpoints(current, endpoints) {
				continue
			}
			delete(rt.routes, agent)
			removed = append(removed, agent)
		}
		if len(removed) == 0 {
			return nil
		}
		return rt.save()
	})
	if err != nil {
		log.Debug("failed to remove stale routes", "error", err)
		return nil
	}
	return removed
}

// anyReachable reports whether at least one endpoint accepts a TCP connection.
// An agent with no endpoints is treated as unreachable — there is nothing to
// serve, so the entry is dead weight in the discovery index.
func anyReachable(endpoints map[string]string) bool {
	for _, addr := range endpoints {
		conn, err := net.DialTimeout("tcp", addr, dialTimeout)
		if err == nil {
			_ = conn.Close()
			return true
		}
	}
	return false
}

// sameEndpoints reports whether two endpoint maps are identical.
func sameEndpoints(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for name, addr := range a {
		if b[name] != addr {
			return false
		}
	}
	return true
}

// Endpoints returns a copy of an agent's endpoint -> host:port map.
// It reloads routes from disk to pick up changes from other processes.
// Returns nil if the agent has no registered routes.
func (rt *RouteTable) Endpoints(agent string) map[string]string {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.reloadForRead()

	endpoints, ok := rt.routes[agent]
	if !ok {
		return nil
	}
	out := make(map[string]string, len(endpoints))
	for name, addr := range endpoints {
		out[name] = addr
	}
	return out
}

// Snapshot returns a deep copy of the full agent -> endpoint -> host:port map.
// It reloads routes from disk to pick up changes from other processes.
func (rt *RouteTable) Snapshot() map[string]map[string]string {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.reloadForRead()

	out := make(map[string]map[string]string, len(rt.routes))
	for agent, endpoints := range rt.routes {
		copied := make(map[string]string, len(endpoints))
		for name, addr := range endpoints {
			copied[name] = addr
		}
		out[agent] = copied
	}
	return out
}

// Agents returns all registered agent names.
// It reloads routes from disk to pick up changes from other processes.
func (rt *RouteTable) Agents() []string {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.reloadForRead()

	agents := make([]string, 0, len(rt.routes))
	for agent := range rt.routes {
		agents = append(agents, agent)
	}
	return agents
}

func (rt *RouteTable) save() error {
	data, err := json.MarshalIndent(rt.routes, "", "  ")
	if err != nil {
		return err
	}
	// Write atomically (unique temp + rename) so a crash mid-write can't leave a
	// truncated routes.json that every proxy then fails to parse. Readers do not
	// take the file lock — the rename is what makes a concurrent read see either
	// the old file or the new one, never a partial write. The temp name is
	// unique per writer so a save racing a reader's directory scan can't collide.
	path := filepath.Join(rt.dir, "routes.json")
	tmp, err := os.CreateTemp(rt.dir, "routes-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	// Match the previous routes.json mode (CreateTemp defaults to 0600).
	if err := os.Chmod(tmpName, 0o644); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
