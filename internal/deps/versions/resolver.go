// Package versions provides version resolution for runtime dependencies.
// It fetches available versions from upstream APIs and resolves partial
// version specifications (e.g., "1.22") to full versions (e.g., "1.22.12").
package versions

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// Resolver resolves partial version specifications to full versions.
type Resolver interface {
	// Resolve turns a partial version into a full version.
	// For example, "1.22" might resolve to "1.22.12".
	// If version is already fully specified and valid, it returns as-is.
	// Returns an error if the version doesn't exist or can't be resolved.
	Resolve(ctx context.Context, version string) (string, error)

	// Available returns all available stable versions, newest first.
	Available(ctx context.Context) ([]string, error)

	// LatestStable returns the latest stable version.
	LatestStable(ctx context.Context) (string, error)
}

// ResolverFor returns the appropriate resolver for a runtime dependency.
// Returns nil if no resolver exists for the given dependency.
func ResolverFor(dep string) Resolver {
	switch dep {
	case "go":
		return &GoResolver{}
	case "node":
		return &NodeResolver{}
	case "python":
		return &PythonResolver{}
	default:
		return nil
	}
}

// isFullVersion checks if a version string appears to be fully specified.
// For Go: 1.22.12 is full, 1.22 is partial
// For Node: 20.11.0 is full, 20 is partial
func isFullVersion(version string, minParts int) bool {
	parts := strings.Split(version, ".")
	return len(parts) >= minParts
}

// semverRegex matches semantic version patterns
var semverRegex = regexp.MustCompile(`^(\d+)\.(\d+)(?:\.(\d+))?$`)

// parseSemver extracts major, minor, patch from a version string.
// Returns major, minor, patch, ok. Patch is -1 if not specified.
func parseSemver(version string) (major, minor, patch int, ok bool) {
	matches := semverRegex.FindStringSubmatch(version)
	if matches == nil {
		return 0, 0, 0, false
	}

	fmt.Sscanf(matches[1], "%d", &major)
	fmt.Sscanf(matches[2], "%d", &minor)

	if matches[3] != "" {
		fmt.Sscanf(matches[3], "%d", &patch)
	} else {
		patch = -1
	}

	return major, minor, patch, true
}
