// Package routing provides hostname-based reverse proxy routing.
package routing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// RouteTable manages agent -> service -> host:port mappings.
type RouteTable struct {
	dir    string
	routes map[string]map[string]string // agent -> service -> host:port
	mu     sync.RWMutex
}

// NewRouteTable creates or loads a route table from the given directory.
func NewRouteTable(dir string) (*RouteTable, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	rt := &RouteTable{
		dir:    dir,
		routes: make(map[string]map[string]string),
	}

	// Load existing routes
	path := filepath.Join(dir, "routes.json")
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &rt.routes)
	}

	return rt, nil
}

// Add registers routes for an agent.
func (rt *RouteTable) Add(agent string, services map[string]string) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.routes[agent] = services
	return rt.save()
}

// Remove unregisters an agent's routes.
func (rt *RouteTable) Remove(agent string) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	delete(rt.routes, agent)
	return rt.save()
}

// Lookup returns the host:port for an agent's service.
func (rt *RouteTable) Lookup(agent, service string) (string, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	services, ok := rt.routes[agent]
	if !ok {
		return "", false
	}
	addr, ok := services[service]
	return addr, ok
}

// LookupDefault returns the first service's address for an agent.
func (rt *RouteTable) LookupDefault(agent string) (string, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	services, ok := rt.routes[agent]
	if !ok || len(services) == 0 {
		return "", false
	}
	// Return first service (map iteration order is random but consistent for small maps)
	for _, addr := range services {
		return addr, true
	}
	return "", false
}

// AgentExists returns true if the agent has registered routes.
func (rt *RouteTable) AgentExists(agent string) bool {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	_, ok := rt.routes[agent]
	return ok
}

// Agents returns all registered agent names.
func (rt *RouteTable) Agents() []string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

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
	path := filepath.Join(rt.dir, "routes.json")
	return os.WriteFile(path, data, 0644)
}
