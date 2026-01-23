// Package deps provides dependency management for moat containers.
package deps

import (
	"context"

	"github.com/andybons/moat/internal/deps/versions"
)

// versionCache is the global cache for resolved versions.
// Initialized lazily on first use.
var versionCache *versions.Cache

// getVersionCache returns the global version cache, initializing it if needed.
func getVersionCache() *versions.Cache {
	if versionCache == nil {
		versionCache = versions.DefaultCache()
	}
	return versionCache
}

// ResolveVersions resolves partial version specifications to full versions
// for runtime dependencies (go, node, python). This uses a cache to avoid
// repeated API calls - resolved versions are cached for 24 hours.
//
// For non-runtime dependencies or those without version resolvers, the
// original version is preserved unchanged.
//
// Example: "go@1.22" might resolve to "go@1.22.12"
func ResolveVersions(ctx context.Context, deps []Dependency) ([]Dependency, error) {
	cache := getVersionCache()
	result := make([]Dependency, len(deps))

	for i, dep := range deps {
		result[i] = dep // Copy the dependency

		// Skip if no version specified or not a runtime
		if dep.Version == "" {
			continue
		}

		spec, ok := GetSpec(dep.Name)
		if !ok || spec.Type != TypeRuntime {
			continue
		}

		// Get cached resolver for this runtime
		resolver := versions.CachedResolverFor(dep.Name, cache)
		if resolver == nil {
			continue
		}

		// Resolve the version
		resolved, err := resolver.Resolve(ctx, dep.Version)
		if err != nil {
			// If resolution fails, keep the original version
			// This allows users to specify exact versions that may not be in APIs
			continue
		}

		result[i].Version = resolved
	}

	return result, nil
}

// SetVersionCache allows tests to inject a custom cache.
func SetVersionCache(c *versions.Cache) {
	versionCache = c
}
