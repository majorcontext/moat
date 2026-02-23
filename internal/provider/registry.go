package provider

import (
	"sort"
	"sync"
)

var (
	mu        sync.RWMutex
	providers = make(map[string]CredentialProvider)
	aliases   = make(map[string]string) // alias -> canonical name
)

// Register adds a provider to the registry.
func Register(p CredentialProvider) {
	mu.Lock()
	defer mu.Unlock()
	providers[p.Name()] = p
}

// RegisterAlias registers an alternative name for a provider.
// This allows looking up a provider by either its canonical name or any alias.
// For example: RegisterAlias("anthropic", "claude") allows Get("anthropic")
// to return the "claude" provider.
func RegisterAlias(alias, canonical string) {
	mu.Lock()
	defer mu.Unlock()
	aliases[alias] = canonical
}

// ResolveName returns the canonical provider name for a given name or alias.
// If the name is directly registered or unknown, it is returned as-is.
// If the name is an alias, the canonical name is returned.
func ResolveName(name string) string {
	mu.RLock()
	defer mu.RUnlock()
	if canonical, ok := aliases[name]; ok {
		return canonical
	}
	return name
}

// Get returns a provider by name or alias, or nil if not found.
func Get(name string) CredentialProvider {
	mu.RLock()
	defer mu.RUnlock()
	// Check direct registration first
	if p, ok := providers[name]; ok {
		return p
	}
	// Check aliases
	if canonical, ok := aliases[name]; ok {
		return providers[canonical]
	}
	return nil
}

// GetAgent returns an AgentProvider by name.
// Returns nil if not found or not an agent provider.
func GetAgent(name string) AgentProvider {
	p := Get(name)
	if agent, ok := p.(AgentProvider); ok {
		return agent
	}
	return nil
}

// GetEndpoint returns an EndpointProvider by name.
// Returns nil if not found or not an endpoint provider.
func GetEndpoint(name string) EndpointProvider {
	p := Get(name)
	if ep, ok := p.(EndpointProvider); ok {
		return ep
	}
	return nil
}

// All returns all registered providers.
func All() []CredentialProvider {
	mu.RLock()
	defer mu.RUnlock()
	result := make([]CredentialProvider, 0, len(providers))
	for _, p := range providers {
		result = append(result, p)
	}
	return result
}

// Agents returns all providers that implement AgentProvider.
func Agents() []AgentProvider {
	mu.RLock()
	defer mu.RUnlock()
	var result []AgentProvider
	for _, p := range providers {
		if agent, ok := p.(AgentProvider); ok {
			result = append(result, agent)
		}
	}
	return result
}

// Names returns the names of all registered providers, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Clear removes all registered providers and aliases. For testing only.
func Clear() {
	mu.Lock()
	defer mu.Unlock()
	providers = make(map[string]CredentialProvider)
	aliases = make(map[string]string)
}
