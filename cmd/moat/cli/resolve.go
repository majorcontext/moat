package cli

import (
	"bufio"
	"fmt"
	"os"
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
// run (e.g., logs, trace, audit, attach).
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
