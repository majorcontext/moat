package versions

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// PythonResolver resolves Python versions.
// Unlike Go and Node, Python doesn't have a simple JSON API for versions.
// We use a hardcoded list of commonly available versions that can be installed
// via deadsnakes PPA or official Docker images.
type PythonResolver struct{}

// pythonVersions lists available Python versions.
// These correspond to versions available in:
// - Official Python Docker images (python:X.Y-slim)
// - Ubuntu deadsnakes PPA
// - pyenv
//
// Sorted newest first within each minor version.
var pythonVersions = []string{
	// 3.13 series
	"3.13.1", "3.13.0",
	// 3.12 series
	"3.12.8", "3.12.7", "3.12.6", "3.12.5", "3.12.4", "3.12.3", "3.12.2", "3.12.1", "3.12.0",
	// 3.11 series
	"3.11.11", "3.11.10", "3.11.9", "3.11.8", "3.11.7", "3.11.6", "3.11.5", "3.11.4", "3.11.3", "3.11.2", "3.11.1", "3.11.0",
	// 3.10 series
	"3.10.16", "3.10.15", "3.10.14", "3.10.13", "3.10.12", "3.10.11", "3.10.10", "3.10.9", "3.10.8", "3.10.7", "3.10.6", "3.10.5", "3.10.4", "3.10.3", "3.10.2", "3.10.1", "3.10.0",
	// 3.9 series
	"3.9.21", "3.9.20", "3.9.19", "3.9.18", "3.9.17", "3.9.16", "3.9.15", "3.9.14", "3.9.13", "3.9.12", "3.9.11", "3.9.10", "3.9.9", "3.9.8", "3.9.7", "3.9.6", "3.9.5", "3.9.4", "3.9.3", "3.9.2", "3.9.1", "3.9.0",
	// 3.8 series (security fixes only)
	"3.8.20", "3.8.19", "3.8.18", "3.8.17", "3.8.16", "3.8.15", "3.8.14", "3.8.13", "3.8.12", "3.8.11", "3.8.10", "3.8.9", "3.8.8", "3.8.7", "3.8.6", "3.8.5", "3.8.4", "3.8.3", "3.8.2", "3.8.1", "3.8.0",
}

// Resolve resolves a Python version specification to a full version.
// Examples:
//   - "3.11" -> "3.11.11" (latest patch)
//   - "3.12" -> "3.12.8" (latest patch)
//   - "3.11.5" -> "3.11.5" (exact, verified to exist)
func (r *PythonResolver) Resolve(ctx context.Context, version string) (string, error) {
	// Parse the requested version
	parts := strings.Split(version, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return "", fmt.Errorf("invalid Python version format %q: expected X.Y or X.Y.Z", version)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return "", fmt.Errorf("invalid Python major version %q", parts[0])
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", fmt.Errorf("invalid Python minor version %q", parts[1])
	}

	var patch int = -1
	if len(parts) == 3 {
		patch, err = strconv.Atoi(parts[2])
		if err != nil {
			return "", fmt.Errorf("invalid Python patch version %q", parts[2])
		}
	}

	// If fully specified, verify it exists
	if patch >= 0 {
		for _, v := range pythonVersions {
			if v == version {
				return version, nil
			}
		}
		return "", fmt.Errorf("Python version %s not found in available versions", version)
	}

	// Find latest patch for major.minor
	prefix := fmt.Sprintf("%d.%d.", major, minor)
	var candidates []string
	for _, v := range pythonVersions {
		if strings.HasPrefix(v, prefix) {
			candidates = append(candidates, v)
		}
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("no Python %d.%d.x releases found", major, minor)
	}

	// Sort by patch version descending
	sort.Slice(candidates, func(i, j int) bool {
		return compareVersions(candidates[i], candidates[j]) > 0
	})

	return candidates[0], nil
}

// Available returns all Python versions, newest first.
func (r *PythonResolver) Available(ctx context.Context) ([]string, error) {
	// Return a copy to prevent modification
	versions := make([]string, len(pythonVersions))
	copy(versions, pythonVersions)
	return versions, nil
}

// LatestStable returns the latest stable Python version.
func (r *PythonResolver) LatestStable(ctx context.Context) (string, error) {
	if len(pythonVersions) == 0 {
		return "", fmt.Errorf("no Python versions available")
	}
	return pythonVersions[0], nil
}
