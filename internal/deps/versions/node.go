package versions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const nodeVersionsURL = "https://nodejs.org/dist/index.json"

// NodeResolver resolves Node.js versions using the nodejs.org API.
type NodeResolver struct {
	// HTTPClient is the HTTP client to use. If nil, http.DefaultClient is used.
	HTTPClient *http.Client
}

// nodeRelease represents a Node.js release from the API.
type nodeRelease struct {
	Version  string `json:"version"` // e.g., "v20.11.0"
	LTS      any    `json:"lts"`     // false or string like "Iron"
	Security bool   `json:"security"`
	Date     string `json:"date"`
}

// Resolve resolves a Node.js version specification to a full version.
// Examples:
//   - "20" -> "20.11.0" (latest in major 20)
//   - "20.11" -> "20.11.1" (latest patch)
//   - "20.11.0" -> "20.11.0" (exact, verified to exist)
func (r *NodeResolver) Resolve(ctx context.Context, version string) (string, error) {
	releases, err := r.fetchReleases(ctx)
	if err != nil {
		return "", fmt.Errorf("fetching Node.js releases: %w", err)
	}

	// Parse the requested version
	parts := strings.Split(version, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return "", fmt.Errorf("invalid Node.js version format %q", version)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return "", fmt.Errorf("invalid Node.js major version %q", parts[0])
	}

	var minor, patch int = -1, -1
	if len(parts) >= 2 {
		minor, err = strconv.Atoi(parts[1])
		if err != nil {
			return "", fmt.Errorf("invalid Node.js minor version %q", parts[1])
		}
	}
	if len(parts) == 3 {
		patch, err = strconv.Atoi(parts[2])
		if err != nil {
			return "", fmt.Errorf("invalid Node.js patch version %q", parts[2])
		}
	}

	// If fully specified, verify it exists
	if patch >= 0 {
		fullVersion := fmt.Sprintf("v%s", version)
		for _, rel := range releases {
			if rel.Version == fullVersion {
				return version, nil
			}
		}
		return "", fmt.Errorf("Node.js version %s not found", version)
	}

	// Find latest matching version
	var candidates []string
	for _, rel := range releases {
		v := strings.TrimPrefix(rel.Version, "v")
		relParts := strings.Split(v, ".")
		if len(relParts) < 3 {
			continue
		}

		relMajor, _ := strconv.Atoi(relParts[0])
		relMinor, _ := strconv.Atoi(relParts[1])

		if relMajor != major {
			continue
		}

		// If minor specified, must match
		if minor >= 0 && relMinor != minor {
			continue
		}

		candidates = append(candidates, v)
	}

	if len(candidates) == 0 {
		if minor >= 0 {
			return "", fmt.Errorf("no Node.js %d.%d.x releases found", major, minor)
		}
		return "", fmt.Errorf("no Node.js %d.x releases found", major)
	}

	// Sort by version descending (API is already sorted, but be safe)
	sort.Slice(candidates, func(i, j int) bool {
		return compareVersions(candidates[i], candidates[j]) > 0
	})

	return candidates[0], nil
}

// Available returns all Node.js versions, newest first.
func (r *NodeResolver) Available(ctx context.Context) ([]string, error) {
	releases, err := r.fetchReleases(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching Node.js releases: %w", err)
	}

	var versions []string
	for _, rel := range releases {
		v := strings.TrimPrefix(rel.Version, "v")
		versions = append(versions, v)
	}

	return versions, nil
}

// LatestStable returns the latest LTS Node.js version.
func (r *NodeResolver) LatestStable(ctx context.Context) (string, error) {
	releases, err := r.fetchReleases(ctx)
	if err != nil {
		return "", fmt.Errorf("fetching Node.js releases: %w", err)
	}

	// Find first LTS release (API returns newest first)
	for _, rel := range releases {
		if rel.LTS != false && rel.LTS != nil {
			return strings.TrimPrefix(rel.Version, "v"), nil
		}
	}

	// Fallback to latest if no LTS
	if len(releases) > 0 {
		return strings.TrimPrefix(releases[0].Version, "v"), nil
	}

	return "", fmt.Errorf("no Node.js releases found")
}

func (r *NodeResolver) fetchReleases(ctx context.Context) ([]nodeRelease, error) {
	client := r.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", nodeVersionsURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, nodeVersionsURL)
	}

	var releases []nodeRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return releases, nil
}

// compareVersions compares two semver strings.
// Returns positive if a > b, negative if a < b, zero if equal.
func compareVersions(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	for i := 0; i < 3; i++ {
		var aVal, bVal int
		if i < len(aParts) {
			aVal, _ = strconv.Atoi(aParts[i])
		}
		if i < len(bParts) {
			bVal, _ = strconv.Atoi(bParts[i])
		}
		if aVal != bVal {
			return aVal - bVal
		}
	}
	return 0
}
