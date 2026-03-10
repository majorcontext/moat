// Package deps provides dependency management for moat containers.
package deps

import (
	"context"

	"github.com/majorcontext/moat/internal/deps/versions"
	"github.com/majorcontext/moat/internal/log"
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

		spec, ok := GetSpec(dep.Name)
		if !ok || spec.Type != TypeRuntime {
			continue
		}

		// If no version specified, use the registry default.
		// This ensures default versions like "1.25" get resolved to a full
		// patch version (e.g., "1.25.8"), preventing broken tarball URLs
		// when the runtime is installed via curl rather than a base image.
		version := dep.Version
		if version == "" {
			version = spec.Default
			if version == "" {
				continue
			}
		}

		// Populate the version and OriginalVersion into the result now, so
		// even if resolution fails below, the Dockerfile generator sees the
		// default rather than an empty string, and selectBaseImage can use
		// OriginalVersion for Docker Hub floating tags.
		result[i].Version = version
		if dep.Version == "" {
			result[i].OriginalVersion = version
		}

		// Get cached resolver for this runtime
		resolver := versions.CachedResolverFor(dep.Name, cache)
		if resolver == nil {
			continue
		}

		// Resolve the version
		resolved, err := resolver.Resolve(ctx, version)
		if err != nil {
			// Resolution failed — the partial version (e.g., "1.25") may
			// produce a broken tarball URL at build time. Log a warning so
			// the user knows resolution was skipped.
			log.Warn("version resolution failed, using unresolved version",
				"runtime", dep.Name, "version", version, "error", err)
			continue
		}

		result[i].OriginalVersion = version
		result[i].Version = resolved
	}

	return result, nil
}

// SetVersionCache allows tests to inject a custom cache.
func SetVersionCache(c *versions.Cache) {
	versionCache = c
}
