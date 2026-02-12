package run

import (
	"fmt"
	"sort"
	"strings"

	"github.com/majorcontext/moat/internal/id"
)

// Resolve takes a user-provided argument (a run ID, ID prefix, or run name)
// and returns the matching run(s).
//
// Resolution priority:
//  1. Exact ID match — if arg is a valid full run ID, look it up directly.
//  2. ID prefix match — if arg starts with "run_", scan for runs whose ID
//     starts with arg. Useful for typing abbreviated IDs.
//  3. Exact name match — scan all runs for those whose Name field equals arg.
//
// Returns an error if no runs match. The caller is responsible for handling
// the case where multiple runs match (e.g., prompting the user).
func (m *Manager) Resolve(arg string) ([]*Run, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 1. Exact ID match
	if id.IsValid(arg, "run") {
		r, ok := m.runs[arg]
		if !ok {
			return nil, fmt.Errorf("run %s not found", arg)
		}
		return []*Run{r}, nil
	}

	// 2. ID prefix match (arg starts with "run_" but isn't a full ID)
	if strings.HasPrefix(arg, "run_") {
		var matches []*Run
		for _, r := range m.runs {
			if strings.HasPrefix(r.ID, arg) {
				matches = append(matches, r)
			}
		}
		if len(matches) > 0 {
			sortRunsByCreatedAt(matches)
			return matches, nil
		}
		// Fall through to name match
	}

	// 3. Exact name match
	var matches []*Run
	for _, r := range m.runs {
		if r.Name == arg {
			matches = append(matches, r)
		}
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("no run found matching %q\n\nRun 'moat list' to see available runs.", arg)
	}

	sortRunsByCreatedAt(matches)
	return matches, nil
}

// sortRunsByCreatedAt sorts runs newest first.
func sortRunsByCreatedAt(runs []*Run) {
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
}
