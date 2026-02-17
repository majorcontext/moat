package versions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
)

const pythonVersionsURL = "https://endoflife.date/api/python.json"

// PythonResolver resolves Python versions using the endoflife.date API.
type PythonResolver struct {
	// HTTPClient is the HTTP client to use. If nil, http.DefaultClient is used.
	HTTPClient *http.Client

	// url overrides the API endpoint. Used for testing. If empty, pythonVersionsURL is used.
	url string
}

// pythonCycle represents a Python release cycle from the endoflife.date API.
type pythonCycle struct {
	Cycle  string `json:"cycle"`  // e.g., "3.13"
	Latest string `json:"latest"` // e.g., "3.13.1"
}

// Resolve resolves a Python version specification to a full version.
// Examples:
//   - "3.11" -> "3.11.11" (latest patch)
//   - "3.12" -> "3.12.8" (latest patch)
//   - "3.11.5" -> "3.11.5" (exact, verified to exist)
func (r *PythonResolver) Resolve(ctx context.Context, version string) (string, error) {
	cycles, err := r.fetchCycles(ctx)
	if err != nil {
		return "", fmt.Errorf("fetching Python releases: %w", err)
	}
	return resolvePythonVersion(version, cycles)
}

// Available returns all Python 3.x versions, newest first.
func (r *PythonResolver) Available(ctx context.Context) ([]string, error) {
	cycles, err := r.fetchCycles(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching Python releases: %w", err)
	}
	return pythonVersionsFromCycles(cycles), nil
}

// LatestStable returns the latest stable Python version.
func (r *PythonResolver) LatestStable(ctx context.Context) (string, error) {
	cycles, err := r.fetchCycles(ctx)
	if err != nil {
		return "", fmt.Errorf("fetching Python releases: %w", err)
	}
	return latestStablePython(cycles)
}

func (r *PythonResolver) apiURL() string {
	if r.url != "" {
		return r.url
	}
	return pythonVersionsURL
}

func (r *PythonResolver) fetchCycles(ctx context.Context) ([]pythonCycle, error) {
	client := r.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	// Always create a bounded context to prevent hangs.
	// Use the minimum of existing deadline and our timeout.
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	url := r.apiURL()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}

	var cycles []pythonCycle
	if err := json.NewDecoder(resp.Body).Decode(&cycles); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return cycles, nil
}

// resolvePythonVersion resolves a version string against a list of Python cycles.
func resolvePythonVersion(version string, cycles []pythonCycle) (string, error) {
	major, minor, patch, ok := parseSemver(version)
	if !ok {
		return "", fmt.Errorf("invalid Python version format %q: expected X.Y or X.Y.Z", version)
	}

	// Find the matching cycle
	cycleStr := fmt.Sprintf("%d.%d", major, minor)
	var matched *pythonCycle
	for i := range cycles {
		if cycles[i].Cycle == cycleStr {
			matched = &cycles[i]
			break
		}
	}

	if matched == nil {
		if patch >= 0 {
			return "", fmt.Errorf("Python version %s not found in available versions", version)
		}
		return "", fmt.Errorf("no Python %d.%d.x releases found", major, minor)
	}

	// Validate the Latest field from the API before using it.
	_, _, latestPatch, latestOK := parseSemver(matched.Latest)
	if !latestOK || latestPatch < 0 {
		return "", fmt.Errorf("malformed version %q in API response for cycle %s", matched.Latest, matched.Cycle)
	}

	// If fully specified, verify it exists.
	// CPython patches are sequential (0, 1, 2, ...), so a patch version exists
	// if its number is between 0 and the latest known patch.
	if patch >= 0 {
		if patch <= latestPatch {
			return version, nil
		}
		return "", fmt.Errorf("Python version %s not found in available versions", version)
	}

	// Partial version: return latest patch
	return matched.Latest, nil
}

// pythonVersionsFromCycles generates all Python 3.x version strings from cycle data,
// sorted newest first (by minor descending, then patch descending).
func pythonVersionsFromCycles(cycles []pythonCycle) []string {
	py3 := filterPython3Cycles(cycles)

	// Sort cycles by minor version descending
	sort.Slice(py3, func(i, j int) bool {
		_, mi, _, _ := parseSemver(py3[i].Cycle)
		_, mj, _, _ := parseSemver(py3[j].Cycle)
		return mi > mj
	})

	var versions []string
	for _, c := range py3 {
		_, _, latestPatch, ok := parseSemver(c.Latest)
		if !ok || latestPatch < 0 {
			continue
		}
		// Generate all patches from latest down to 0
		for p := latestPatch; p >= 0; p-- {
			versions = append(versions, fmt.Sprintf("%s.%d", c.Cycle, p))
		}
	}
	return versions
}

// latestStablePython returns the latest stable Python 3.x version from cycle data.
func latestStablePython(cycles []pythonCycle) (string, error) {
	py3 := filterPython3Cycles(cycles)
	if len(py3) == 0 {
		return "", fmt.Errorf("no Python 3.x releases found")
	}

	// Sort by minor version descending to find newest
	sort.Slice(py3, func(i, j int) bool {
		_, mi, _, _ := parseSemver(py3[i].Cycle)
		_, mj, _, _ := parseSemver(py3[j].Cycle)
		return mi > mj
	})

	return py3[0].Latest, nil
}

// filterPython3Cycles returns only Python 3.x cycles.
func filterPython3Cycles(cycles []pythonCycle) []pythonCycle {
	var py3 []pythonCycle
	for _, c := range cycles {
		major, _, _, ok := parseSemver(c.Cycle)
		if ok && major == 3 {
			py3 = append(py3, c)
		}
	}
	return py3
}
