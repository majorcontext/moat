// Package secrets provides pluggable secret resolution from external backends.
package secrets

import (
	"context"
	"fmt"
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

// ResolveAll resolves all secrets in the map, returning resolved values.
// Keys are environment variable names, values are secret references.
// Fails fast on first error.
func ResolveAll(ctx context.Context, secrets map[string]string) (map[string]string, error) {
	if len(secrets) == 0 {
		return nil, nil
	}

	resolved := make(map[string]string, len(secrets))
	for name, ref := range secrets {
		val, err := Resolve(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("resolving secret %s: %w", name, err)
		}
		resolved[name] = val
	}
	return resolved, nil
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
