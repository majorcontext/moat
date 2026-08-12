package cli

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/run"
)

// runHostsAgent reports whether r can host agentArg.
//
// It applies the same nil-vs-empty rule as validateJoinAgent: a nil capability
// set means the run predates capability tracking, so fall back to the recorded
// agent string. Without this fallback the shorthand would report "no running
// runs can host claude" for the entire pre-upgrade population while the
// explicit two-arg form succeeded on the same run.
func runHostsAgent(r *run.Run, agentArg string, j provider.JoinableAgent) bool {
	if r.JoinableAgents != nil {
		for _, a := range r.JoinableAgents {
			if a == agentArg || j.IdentifiesAs(a) {
				return true
			}
		}
		return false
	}
	return j.IdentifiesAs(r.Agent)
}

// hostedAgents returns the agent names to show in the picker's AGENTS column,
// deriving them from the recorded agent string for pre-upgrade runs.
func hostedAgents(r *run.Run) []string {
	if r.JoinableAgents != nil {
		return r.JoinableAgents
	}
	if r.Agent != "" {
		return []string{r.Agent}
	}
	return nil
}

// inferJoinCandidates narrows runs to those that can host agentArg, preferring
// the current workspace. widened reports that no run in cwd qualified and the
// search covered every running run — the caller must disclose that, because
// attaching to another workspace's run means using that run's grants.
//
// Results are sorted newest-first, matching every other multi-match surface
// in this CLI (resolve.go's SortRunsByCreatedAt). runs typically comes from
// manager.List(), which iterates a map and is unordered per call — without
// this sort, renderPicker's slice-index numbering would shuffle between
// invocations, so a number the user remembers from one run of the picker
// could attach to a different run (and, when widened, a different
// workspace's grants) the next time.
func inferJoinCandidates(runs []*run.Run, cwd, agentArg string, j provider.JoinableAgent) (candidates []*run.Run, widened bool) {
	var all []*run.Run
	for _, r := range runs {
		if r.GetState() != run.StateRunning {
			continue
		}
		if !runHostsAgent(r, agentArg, j) {
			continue
		}
		all = append(all, r)
	}
	run.SortRunsByCreatedAt(all)

	var local []*run.Run
	for _, r := range all {
		if r.Workspace == cwd {
			local = append(local, r)
		}
	}
	if len(local) > 0 {
		return local, false
	}
	return all, len(all) > 0
}

// renderPicker writes the numbered candidate table.
//
// Writes to the caller-supplied writer, which is os.Stderr in production —
// matching printMatchingRuns and disambiguateRuns. CLAUDE.md's "write command
// output to stdout" rule covers results, not interactive prompts: a picker on
// stdout hangs invisibly under `moat join claude | tee log` when stdin is still
// a TTY. No ui style functions inside the tabwriter — ANSI codes break column
// alignment.
func renderPicker(w io.Writer, candidates []*run.Run, agentArg string, widened bool) {
	if widened {
		fmt.Fprintf(w, "No running runs in this workspace can host %s — showing all running runs:\n\n", agentArg)
	} else {
		fmt.Fprintf(w, "Multiple running runs can host %s:\n\n", agentArg)
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if widened {
		fmt.Fprintln(tw, "    NAME\tRUN ID\tAGENTS\tAGE\tWORKSPACE")
	} else {
		fmt.Fprintln(tw, "    NAME\tRUN ID\tAGENTS\tAGE")
	}
	for i, r := range candidates {
		agents := strings.Join(hostedAgents(r), ", ")
		if agents == "" {
			agents = "-"
		}
		if widened {
			fmt.Fprintf(tw, "  %d %s\t%s\t%s\t%s\t%s\n",
				i+1, r.Name, r.ID, agents, formatTimeAgo(r.CreatedAt), r.Workspace)
			continue
		}
		fmt.Fprintf(tw, "  %d %s\t%s\t%s\t%s\n",
			i+1, r.Name, r.ID, agents, formatTimeAgo(r.CreatedAt))
	}
	tw.Flush()
	fmt.Fprintln(w)
}

// readSelection reads a 1-based choice in [1, n].
//
// Invalid input aborts rather than re-prompting, matching disambiguateRuns's
// abort-on-invalid-input convention. Re-prompting in a loop is a trap for
// scripted callers whose stdin never produces a valid answer.
func readSelection(r io.Reader, n int) (int, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && line == "" {
		return 0, fmt.Errorf("no selection made; run `moat join <run> <agent>` to specify directly")
	}
	choice, convErr := strconv.Atoi(strings.TrimSpace(line))
	if convErr != nil || choice < 1 || choice > n {
		return 0, fmt.Errorf("invalid selection %q; run `moat join <run> <agent>` to specify directly",
			strings.TrimSpace(line))
	}
	return choice, nil
}

// pickJoinRun resolves a candidate list to one run.
//
// A single candidate auto-selects UNLESS the search was widened past the
// current workspace: attaching to another workspace's run silently borrows that
// run's grants and network policy, so it is confirmed rather than assumed.
//
// anyRunning distinguishes the two zero-candidate causes, which imply
// different next steps: no running runs anywhere (start one) vs. running runs
// exist but none can host agentArg (add it to moat.yaml's agents: list and
// recreate). candidates alone can't tell these apart — by the time it is
// empty, inferJoinCandidates has already filtered by capability across every
// running run, local or not — so the caller must supply the distinction from
// the unfiltered population.
//
// canonical is agentArg resolved through registry aliases (openai -> codex);
// the diagnosis halves of the errors below keep agentArg (what the user
// typed), the remedy halves use canonical, so a suggested `moat <name>` or
// `agents: [<name>]` names a real command/value.
func pickJoinRun(in io.Reader, out io.Writer, candidates []*run.Run, agentArg, canonical string, widened, isTTY, anyRunning bool) (*run.Run, error) {
	switch len(candidates) {
	case 0:
		if !anyRunning {
			return nil, fmt.Errorf("no runs are running; start one with `moat %s`.", canonical)
		}
		return nil, fmt.Errorf("no running run can host %s.\n"+
			"Add %s to this project's moat.yaml `agents:` list and recreate the run, or run `moat list` to see what is running.",
			agentArg, canonical)
	case 1:
		if !widened {
			return candidates[0], nil
		}
	}

	if !isTTY {
		ids := make([]string, len(candidates))
		for i, r := range candidates {
			ids[i] = r.ID
		}
		return nil, fmt.Errorf("%d running runs can host %s; specify one: %s",
			len(candidates), agentArg, strings.Join(ids, ", "))
	}

	renderPicker(out, candidates, agentArg, widened)
	fmt.Fprintf(out, "Select [1-%d]: ", len(candidates))
	choice, err := readSelection(in, len(candidates))
	if err != nil {
		return nil, err
	}
	return candidates[choice-1], nil
}
