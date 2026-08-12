package cli

import (
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
//
//nolint:unused // wired into the picker's AGENTS column by Task 16
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
//nolint:unparam // cwd is a literal only in today's tests; Task 16 wires runJoin's os.Getwd() through here
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
