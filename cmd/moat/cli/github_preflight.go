package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/githubscope"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/ui"
)

// githubPreflight carries what preflightGitHubScope needs besides the run.
type githubPreflight struct {
	out         io.Writer
	in          *bufio.Reader
	interactive bool
	now         func() time.Time
}

// preflightGitHubScope prints which repositories a scoped run may change and,
// on an interactive terminal, asks about repositories that appeared since the
// last approved run. Approvals are written to the store before the run is
// created, so manager.Create — which recomputes the same plan — grants them.
//
// It never fails the run: anything wrong (a conflicting grant, an unreadable
// store) is reported by manager.Create with the full error.
func preflightGitHubScope(workspace string, grants []string, cfg *config.Config, pf githubPreflight) {
	store, err := run.OpenGitHubScopeStore()
	if err != nil {
		log.Debug("github scope pre-flight: could not open store", "error", err)
		return
	}
	defer store.Close()

	plan, err := run.PlanGitHubScope(store, workspace, grants, cfg)
	if err != nil {
		log.Debug("github scope pre-flight: could not build plan", "error", err)
		return
	}
	if !plan.Enabled || run.GitHubScopeConflict(plan, grants, cfg) != nil {
		return
	}

	printGitHubPlan(pf.out, plan)

	if len(plan.Pending) == 0 {
		return
	}
	other := otherReposPhrase(plan.OtherRepos)
	if !pf.interactive {
		for _, c := range plan.Pending {
			fmt.Fprintf(pf.out, "%s GitHub repository %s (%s) is new to this project and not approved; it is %s this run.\n",
				ui.WarnTag(), c.Repo, strings.Join(c.Sources, ", "), other)
			fmt.Fprintf(pf.out, "  Approve it with: moat github allow %s\n", c.Repo)
		}
		return
	}

	var approved []githubscope.Candidate
	for _, c := range plan.Pending {
		fmt.Fprintf(pf.out, "Allow this run to push to %s? It was found in %s. [y/N]: ", ui.Bold(c.Repo), strings.Join(c.Sources, ", "))
		answer, _ := pf.in.ReadString('\n')
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer == "y" || answer == "yes" {
			approved = append(approved, c)
			continue
		}
		fmt.Fprintf(pf.out, "  Not approved; %s is %s. Approve it later with: moat github allow %s\n", c.Repo, other, c.Repo)
	}
	if len(approved) == 0 {
		return
	}
	if err := githubscope.Approve(store, plan.Project, approved, pf.now()); err != nil {
		fmt.Fprintf(pf.out, "%s could not record approval: %v\n", ui.WarnTag(), err)
	}
}

// printGitHubPlan prints the run's GitHub scope summary.
func printGitHubPlan(w io.Writer, plan *githubscope.Plan) {
	header := "GitHub: this run can only push to these repositories"
	if plan.FirstRun {
		header += " (recorded for future runs)"
	}
	fmt.Fprintln(w, header+":")
	if len(plan.Granted) == 0 {
		fmt.Fprintln(w, "  (none — no GitHub remotes found in the workspace)")
	}
	width := 0
	for _, c := range plan.Granted {
		width = max(width, len(c.Repo))
	}
	for _, c := range plan.Granted {
		fmt.Fprintf(w, "  %-*s  %s\n", width, c.Repo, ui.Dim(strings.Join(c.Sources, ", ")))
	}
	fmt.Fprintf(w, "  Other repositories: %s\n", otherReposPhrase(plan.OtherRepos))
	if plan.Sticky {
		fmt.Fprintf(w, "  %s\n", ui.Dim(fmt.Sprintf("moat.yaml no longer sets github.scope: workspace, but this project was locked on %s, so the scope stays on. To remove it: moat github forget --all",
			plan.LockedAt.Format("2006-01-02"))))
	}
	if plan.Truncated {
		fmt.Fprintf(w, "  %s\n", ui.Dim("The workspace scan stopped early; repositories deeper in the tree were not checked."))
	}
	if len(plan.Pending) > 0 {
		fmt.Fprintln(w, "New since the last approved run:")
		for _, c := range plan.Pending {
			fmt.Fprintf(w, "  %s  %s\n", c.Repo, ui.Dim(strings.Join(c.Sources, ", ")))
		}
	}
}

func otherReposPhrase(level string) string {
	if level == githubscope.OtherReposNone {
		return "blocked (other_repos: none)"
	}
	return "read-only"
}

// defaultGitHubPreflight wires the pre-flight to the terminal.
func defaultGitHubPreflight(interactive bool) githubPreflight {
	return githubPreflight{
		out:         os.Stderr,
		in:          bufio.NewReader(os.Stdin),
		interactive: interactive,
		now:         time.Now,
	}
}
