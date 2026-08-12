package cli

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/term"
)

// resolveRunArg resolves a user-provided argument (name or ID) to one or more
// run IDs using the manager's Resolve method.
//
// When multiple runs match (e.g., multiple runs share the same name):
//   - For TTY sessions: prints matching runs and prompts "Act on all N runs? [y/N]"
//   - For non-TTY (piped/scripted): returns an error asking the user to specify a run ID
//
// The action parameter is used in the prompt (e.g., "Stop", "Destroy").
func resolveRunArg(manager *run.Manager, arg string, action string) ([]string, error) {
	matches, err := manager.Resolve(arg)
	if err != nil {
		return nil, err
	}

	if len(matches) == 1 {
		return []string{matches[0].ID}, nil
	}

	// Multiple matches — need disambiguation
	return disambiguateRuns(matches, arg, action)
}

// resolveRunArgSingle resolves a user-provided argument to exactly one run ID.
// If multiple runs match, it prints them and returns an error telling the user
// to specify a run ID. This is used by commands that only operate on a single
// run (e.g., logs, trace, audit).
func resolveRunArgSingle(manager *run.Manager, arg string) (string, error) {
	matches, err := manager.Resolve(arg)
	if err != nil {
		return "", err
	}

	if len(matches) == 1 {
		return matches[0].ID, nil
	}

	// Multiple matches — print them and error
	printMatchingRuns(matches, arg)
	return "", fmt.Errorf("name %q matches %d runs; specify a run ID to disambiguate", arg, len(matches))
}

// disambiguateRuns handles the multi-match case for batch commands.
func disambiguateRuns(matches []*run.Run, arg string, action string) ([]string, error) {
	printMatchingRuns(matches, arg)

	// Non-TTY: error out instead of prompting
	if !term.IsTerminal(os.Stdin) {
		return nil, fmt.Errorf("name %q matches %d runs; specify a run ID (non-interactive mode)", arg, len(matches))
	}

	// Prompt user
	fmt.Fprintf(os.Stderr, "%s all %d runs? [y/N]: ", action, len(matches))

	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))

	if answer != "y" && answer != "yes" {
		return nil, fmt.Errorf("aborted")
	}

	ids := make([]string, len(matches))
	for i, m := range matches {
		ids[i] = m.ID
	}
	return ids, nil
}

// filterRunning returns only the runs currently in the running state.
func filterRunning(matches []*run.Run) []*run.Run {
	out := make([]*run.Run, 0, len(matches))
	for _, r := range matches {
		if r.GetState() == run.StateRunning {
			out = append(out, r)
		}
	}
	return out
}

// resolveRunningFrom narrows a name/ID match set to running runs.
//
// Returns exactly one of: a single run, a candidate list for the caller to
// disambiguate, or an error. When filtering empties a non-empty match set the
// error names the state ("not running (state: stopped)") rather than degrading
// to "no run found" — the specific cause is what tells the user what to do.
func resolveRunningFrom(matches []*run.Run, arg string) (*run.Run, []*run.Run, error) {
	if len(matches) == 0 {
		return nil, nil, fmt.Errorf("no run found matching %q\n\nRun 'moat list' to see available runs.", arg)
	}
	running := filterRunning(matches)
	if len(running) == 0 {
		sortRunsByCreatedAt(matches)
		r := matches[0]
		return nil, nil, fmt.Errorf("run %s is not running (state: %s)", r.ID, r.GetState())
	}
	if len(running) == 1 {
		return running[0], nil, nil
	}
	sortRunsByCreatedAt(running)
	return nil, running, nil
}

// resolveRunningRunArg resolves a user-supplied run argument to a running run.
func resolveRunningRunArg(manager *run.Manager, arg string) (*run.Run, []*run.Run, error) {
	matches, err := manager.Resolve(arg)
	if err != nil {
		return nil, nil, err
	}
	return resolveRunningFrom(matches, arg)
}

// sortRunsByCreatedAt sorts runs newest first. manager.Resolve already
// returns matches in this order, but resolveRunningFrom is also exercised
// directly with hand-built slices (see resolve_test.go), and filtering
// itself doesn't change ordering — so this keeps both entry points
// consistent. (internal/run has an equivalent helper, but it is unexported
// and not reachable from this package.)
func sortRunsByCreatedAt(matches []*run.Run) {
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].CreatedAt.After(matches[j].CreatedAt)
	})
}

// printMatchingRuns prints a table of matching runs to stderr.
func printMatchingRuns(matches []*run.Run, arg string) {
	fmt.Fprintf(os.Stderr, "Multiple runs match %q:\n", arg)
	w := tabwriter.NewWriter(os.Stderr, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  NAME\tRUN ID\tSTATE\tAGE")
	for _, r := range matches {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n",
			r.Name,
			r.ID,
			r.GetState(),
			formatTimeAgo(r.CreatedAt),
		)
	}
	w.Flush()
	fmt.Fprintln(os.Stderr)
}
