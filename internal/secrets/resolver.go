// Package secrets provides pluggable secret resolution from external backends.
package secrets

import (
	"context"
	"strings"
	"sync"
)

// Resolver resolves a secret reference to its plaintext value.
type Resolver interface {
	// Scheme returns the URI scheme this resolver handles (e.g., "op", "ssm").
	Scheme() string

	// Resolve fetches the secret value for the given reference.
	// The reference is the full URI (e.g., "op://Dev/OpenAI/api-key").
	Resolve(ctx context.Context, reference string) (string, error)
}

var (
	resolvers = make(map[string]Resolver)
	mu        sync.RWMutex
)

// Register adds a resolver to the registry.
func Register(r Resolver) {
	mu.Lock()
	defer mu.Unlock()
	resolvers[r.Scheme()] = r
}

// Resolve dispatches to the appropriate resolver based on URI scheme.
func Resolve(ctx context.Context, reference string) (string, error) {
	scheme := parseScheme(reference)
	if scheme == "" {
		return "", &InvalidReferenceError{Reference: reference, Reason: "missing scheme"}
	}

	mu.RLock()
	r, ok := resolvers[scheme]
	mu.RUnlock()

	if !ok {
		return "", &UnsupportedSchemeError{Scheme: scheme}
	}

	return r.Resolve(ctx, reference)
}

// parseScheme extracts the scheme from a URI (e.g., "op" from "op://vault/item").
func parseScheme(ref string) string {
	idx := strings.Index(ref, "://")
	if idx < 1 {
		return ""
	}
	return ref[:idx]
}

// clearRegistry removes all registered resolvers. For testing only.
func clearRegistry() {
	mu.Lock()
	defer mu.Unlock()
	resolvers = make(map[string]Resolver)
}
