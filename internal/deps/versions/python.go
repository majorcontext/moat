package versions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

const pythonVersionsURL = "https://endoflife.date/api/python.json"

// PythonResolver resolves Python versions using the endoflife.date API.
type PythonResolver struct {
	// HTTPClient is the HTTP client to use. If nil, http.DefaultClient is used.
	HTTPClient *http.Client
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

func (r *PythonResolver) fetchCycles(ctx context.Context) ([]pythonCycle, error) {
	client := r.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	// Always create a bounded context to prevent hangs.
	// Use the minimum of existing deadline and our timeout.
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", pythonVersionsURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, pythonVersionsURL)
	}

	var cycles []pythonCycle
	if err := json.NewDecoder(resp.Body).Decode(&cycles); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return cycles, nil
}

// resolvePythonVersion resolves a version string against a list of Python cycles.
func resolvePythonVersion(version string, cycles []pythonCycle) (string, error) {
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

	patch := -1
	if len(parts) == 3 {
		patch, err = strconv.Atoi(parts[2])
		if err != nil {
			return "", fmt.Errorf("invalid Python patch version %q", parts[2])
		}
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

	// If fully specified, verify it exists.
	// CPython patches are sequential (0, 1, 2, ...), so a patch version exists
	// if its number is between 0 and the latest known patch.
	if patch >= 0 {
		latestParts := strings.Split(matched.Latest, ".")
		if len(latestParts) == 3 {
			latestPatch, _ := strconv.Atoi(latestParts[2])
			if patch <= latestPatch {
				return version, nil
			}
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
		_, mi, _ := parsePythonCycle(py3[i].Cycle)
		_, mj, _ := parsePythonCycle(py3[j].Cycle)
		return mi > mj
	})

	var versions []string
	for _, c := range py3 {
		latestParts := strings.Split(c.Latest, ".")
		if len(latestParts) != 3 {
			continue
		}
		latestPatch, err := strconv.Atoi(latestParts[2])
		if err != nil {
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
		_, mi, _ := parsePythonCycle(py3[i].Cycle)
		_, mj, _ := parsePythonCycle(py3[j].Cycle)
		return mi > mj
	})

	return py3[0].Latest, nil
}

// filterPython3Cycles returns only Python 3.x cycles.
func filterPython3Cycles(cycles []pythonCycle) []pythonCycle {
	var py3 []pythonCycle
	for _, c := range cycles {
		major, _, ok := parsePythonCycle(c.Cycle)
		if ok && major == 3 {
			py3 = append(py3, c)
		}
	}
	return py3
}

// parsePythonCycle parses a cycle string like "3.13" into major, minor.
func parsePythonCycle(cycle string) (major, minor int, ok bool) {
	parts := strings.Split(cycle, ".")
	if len(parts) != 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}
