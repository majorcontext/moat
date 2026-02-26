package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// Registry is a thread-safe mapping of auth tokens to RunContexts.
// It provides the central lookup mechanism for the daemon proxy to
// resolve incoming requests to their per-run configuration and credentials.
type Registry struct {
	mu   sync.RWMutex
	runs map[string]*RunContext // token -> RunContext
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{runs: make(map[string]*RunContext)}
}

// Register adds a RunContext and returns a generated auth token.
// The token is a 32-byte cryptographically random hex string.
func (r *Registry) Register(rc *RunContext) string {
	token := generateToken()
	rc.AuthToken = token
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs[token] = rc
	return token
}

// Lookup finds a RunContext by auth token.
func (r *Registry) Lookup(token string) (*RunContext, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rc, ok := r.runs[token]
	return rc, ok
}

// Unregister removes a RunContext by its auth token.
func (r *Registry) Unregister(token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.runs, token)
}

// UpdateContainerID sets the container ID for a registered run.
// Returns false if the token is not found.
func (r *Registry) UpdateContainerID(token, containerID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rc, ok := r.runs[token]
	if !ok {
		return false
	}
	rc.ContainerID = containerID
	return true
}

// List returns all registered RunContexts.
func (r *Registry) List() []*RunContext {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*RunContext, 0, len(r.runs))
	for _, rc := range r.runs {
		result = append(result, rc)
	}
	return result
}

// Count returns the number of registered runs.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.runs)
}

// generateToken returns a 32-byte cryptographically random hex string.
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
