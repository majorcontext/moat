package versions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

const goVersionsURL = "https://go.dev/dl/?mode=json&include=all"

// GoResolver resolves Go versions using the go.dev API.
type GoResolver struct {
	// HTTPClient is the HTTP client to use. If nil, http.DefaultClient is used.
	HTTPClient *http.Client
}

// goRelease represents a Go release from the API.
type goRelease struct {
	Version string   `json:"version"`
	Stable  bool     `json:"stable"`
	Files   []goFile `json:"files"`
}

type goFile struct {
	Filename string `json:"filename"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Kind     string `json:"kind"`
}

// Resolve resolves a Go version specification to a full version.
// Examples:
//   - "1.22" -> "1.22.12" (latest patch)
//   - "1.25" -> "1.25.6" (latest patch)
//   - "1.22.5" -> "1.22.5" (exact, verified to exist)
func (r *GoResolver) Resolve(ctx context.Context, version string) (string, error) {
	releases, err := r.fetchReleases(ctx)
	if err != nil {
		return "", fmt.Errorf("fetching Go releases: %w", err)
	}

	// Parse the requested version
	major, minor, patch, ok := parseSemver(version)
	if !ok {
		return "", fmt.Errorf("invalid Go version format %q: expected X.Y or X.Y.Z", version)
	}

	// If patch is specified, verify it exists
	if patch >= 0 {
		fullVersion := fmt.Sprintf("go%s", version)
		for _, rel := range releases {
			if rel.Version == fullVersion && rel.Stable {
				return version, nil
			}
		}
		return "", fmt.Errorf("Go version %s not found", version)
	}

	// Find latest patch for major.minor
	prefix := fmt.Sprintf("go%d.%d.", major, minor)
	exactMatch := fmt.Sprintf("go%d.%d", major, minor)

	var candidates []string
	for _, rel := range releases {
		if !rel.Stable {
			continue
		}
		// Match go1.22.X or go1.22 (the .0 release)
		if strings.HasPrefix(rel.Version, prefix) || rel.Version == exactMatch {
			// Extract version without "go" prefix
			v := strings.TrimPrefix(rel.Version, "go")
			candidates = append(candidates, v)
		}
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("no stable Go %d.%d.x releases found", major, minor)
	}

	// Sort by patch version descending
	sort.Slice(candidates, func(i, j int) bool {
		_, _, pi, _ := parseSemver(candidates[i])
		_, _, pj, _ := parseSemver(candidates[j])
		// Treat missing patch as 0
		if pi < 0 {
			pi = 0
		}
		if pj < 0 {
			pj = 0
		}
		return pi > pj
	})

	return candidates[0], nil
}

// Available returns all stable Go versions, newest first.
func (r *GoResolver) Available(ctx context.Context) ([]string, error) {
	releases, err := r.fetchReleases(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching Go releases: %w", err)
	}

	var versions []string
	for _, rel := range releases {
		if rel.Stable {
			v := strings.TrimPrefix(rel.Version, "go")
			versions = append(versions, v)
		}
	}

	return versions, nil
}

// LatestStable returns the latest stable Go version.
func (r *GoResolver) LatestStable(ctx context.Context) (string, error) {
	releases, err := r.fetchReleases(ctx)
	if err != nil {
		return "", fmt.Errorf("fetching Go releases: %w", err)
	}

	// API returns newest first, find first stable
	for _, rel := range releases {
		if rel.Stable {
			return strings.TrimPrefix(rel.Version, "go"), nil
		}
	}

	return "", fmt.Errorf("no stable Go releases found")
}

func (r *GoResolver) fetchReleases(ctx context.Context) ([]goRelease, error) {
	client := r.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", goVersionsURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, goVersionsURL)
	}

	var releases []goRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return releases, nil
}
