package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/githubscope"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/ui"
)

var githubCmd = &cobra.Command{
	Use:   "github",
	Short: "Manage which GitHub repositories this project's runs can change",
	Long: `Manage the GitHub repositories this project's runs may change when moat.yaml
sets github.scope: workspace.

These commands act on the current project: the nearest directory at or above
the current one that contains moat.yaml (or the current directory if there is
none). Approvals are stored outside the project, in ~/.moat/github-repos.json,
so an agent running in the project cannot change them.

On a project's first scoped run, moat approves every GitHub repository it finds
in the workspace. A repository that appears later — a new clone, or a remote
pointed somewhere else — gets read-only access until you approve it, either at
the prompt when a run starts or with 'moat github allow'.

See https://majorcontext.com/moat/guides/github-scope`,
	Args: cobra.NoArgs,
	RunE: githubRepos,
}

var githubReposCmd = &cobra.Command{
	Use:   "repos",
	Short: "Show the repositories this project's runs can push to",
	Args:  cobra.NoArgs,
	RunE:  githubRepos,
}

var githubAllowCmd = &cobra.Command{
	Use:   "allow <owner/repo>...",
	Short: "Let this project's runs push to a repository",
	Long: `Approve repositories for the current project.

A repository approved here is writable by every scoped run in this project,
whether or not it is checked out in the workspace. Approving a repository does
not turn the scope on; set github.scope: workspace in moat.yaml for that.`,
	Args: cobra.MinimumNArgs(1),
	RunE: githubAllow,
}

var githubForgetAll bool

var githubForgetCmd = &cobra.Command{
	Use:   "forget [<owner/repo>...]",
	Short: "Revoke a repository, or reset the project with --all",
	Long: `Remove approved repositories from the current project.

With --all, delete the project's record entirely: its approvals and its lock.
The lock is what keeps github.scope: workspace in force after moat.yaml stops
asking for it, so --all is how you turn the scope off for a project. If
moat.yaml still sets github.scope: workspace, the next run starts over and
approves whatever it finds in the workspace.`,
	RunE: githubForget,
}

var githubPolicyCmd = &cobra.Command{
	Use:   "policy",
	Short: "Print the Keep policy a run in this project would use",
	Long: `Print the Keep rule file the proxy evaluates for this project's runs, filled in
with the approved repositories and their cached GitHub IDs. Runs refresh the IDs
from GitHub when they start; this command makes no network requests.

A run merges any network.keep_policy rules into the same file; they are not
shown here. Save the output and pass it to 'keep test' to check how requests
are decided.`,
	Args: cobra.NoArgs,
	RunE: githubPolicy,
}

func init() {
	githubForgetCmd.Flags().BoolVar(&githubForgetAll, "all", false, "forget every approval and unlock the project")
	githubCmd.AddCommand(githubReposCmd, githubAllowCmd, githubForgetCmd, githubPolicyCmd)
	rootCmd.AddCommand(githubCmd)
}

// githubProject is the current project and its scope state.
type githubProject struct {
	dir      string
	settings githubscope.Settings
	store    *githubscope.Store
}

func openGitHubProject() (*githubProject, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	store, err := run.OpenGitHubScopeStore()
	if err != nil {
		return nil, err
	}
	dir, err := githubscope.FindProject(cwd, store)
	if err != nil {
		store.Close()
		return nil, err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("loading %s/moat.yaml: %w", dir, err)
	}
	var settings githubscope.Settings
	if cfg != nil {
		settings = cfg.GitHub.Settings()
	}
	return &githubProject{dir: dir, settings: settings, store: store}, nil
}

// plan computes the project's plan. When the project is not scoped, it shows
// what a scoped run would approve, so `repos` and `policy` are useful before
// the scope is turned on.
func (p *githubProject) plan() (*githubscope.Plan, *githubscope.Project, error) {
	existing, err := p.store.Get(p.dir)
	if err != nil {
		return nil, nil, err
	}
	plan, err := githubscope.BuildPlan(p.dir, p.settings, existing)
	if err != nil {
		return nil, nil, err
	}
	if !plan.Enabled {
		preview := p.settings
		preview.Scope = githubscope.ScopeWorkspace
		plan, err = githubscope.BuildPlan(p.dir, preview, existing)
		if err != nil {
			return nil, nil, err
		}
		plan.Enabled = false
	}
	return plan, existing, nil
}

func githubRepos(cmd *cobra.Command, _ []string) error {
	p, err := openGitHubProject()
	if err != nil {
		return err
	}
	defer p.store.Close()
	plan, _, err := p.plan()
	if err != nil {
		return err
	}
	writeGitHubRepos(cmd.OutOrStdout(), p.dir, plan)
	return nil
}

func writeGitHubRepos(w io.Writer, dir string, plan *githubscope.Plan) {
	fmt.Fprintf(w, "Project: %s\n", dir)
	switch {
	case !plan.Enabled:
		fmt.Fprintln(w, "Scope:   any — runs can change every repository the github grant can")
		fmt.Fprintln(w, "         Set github.scope: workspace in moat.yaml to limit them to the repositories below.")
	case plan.Sticky:
		fmt.Fprintf(w, "Scope:   workspace (locked %s; moat.yaml no longer sets it — 'moat github forget --all' unlocks)\n", plan.LockedAt.Format("2006-01-02"))
	case plan.FirstRun:
		fmt.Fprintln(w, "Scope:   workspace (not run yet — the next run approves the repositories found)")
	default:
		fmt.Fprintf(w, "Scope:   workspace (since %s)\n", plan.LockedAt.Format("2006-01-02"))
	}
	if plan.Enabled {
		fmt.Fprintf(w, "Other repositories: %s\n", otherReposPhrase(plan.OtherRepos))
	}
	fmt.Fprintln(w)

	type row struct{ repo, status, where string }
	rows := make([]row, 0, len(plan.Granted)+len(plan.Pending)+len(plan.Missing))
	for _, c := range plan.Granted {
		status := "approved"
		switch {
		case !plan.Enabled:
			status = "found"
		case plan.FirstRun:
			status = "approved on next run"
		}
		rows = append(rows, row{c.Repo, status, strings.Join(c.Sources, ", ")})
	}
	for _, c := range plan.Pending {
		rows = append(rows, row{c.Repo, "pending", strings.Join(c.Sources, ", ")})
	}
	for _, r := range plan.Missing {
		rows = append(rows, row{r, "approved", "not in workspace"})
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, "No GitHub repositories found in the workspace.")
		return
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].repo < rows[j].repo })
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "REPOSITORY\tSTATUS\tFOUND IN")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", r.repo, r.status, r.where)
	}
	tw.Flush()
	if len(plan.Pending) > 0 {
		fmt.Fprintf(w, "\nPending repositories are %s. Approve one with: moat github allow OWNER/REPO\n", otherReposPhrase(plan.OtherRepos))
	}
}

func githubAllow(cmd *cobra.Command, args []string) error {
	var cands []githubscope.Candidate
	for _, a := range args {
		repo, err := githubscope.NormalizeRepo(a)
		if err != nil {
			return err
		}
		cands = append(cands, githubscope.Candidate{Repo: repo, Sources: []string{githubscope.SourceAllowCommand}})
	}
	p, err := openGitHubProject()
	if err != nil {
		return err
	}
	defer p.store.Close()
	if approveErr := githubscope.Approve(p.store, p.dir, cands, time.Now()); approveErr != nil {
		return approveErr
	}
	out := cmd.OutOrStdout()
	for _, c := range cands {
		fmt.Fprintf(out, "%s Runs in %s can push to %s\n", ui.OKTag(), p.dir, c.Repo)
	}
	existing, err := p.store.Get(p.dir)
	if err == nil && (existing == nil || existing.LockedAt.IsZero()) && !p.settings.Enabled() {
		fmt.Fprintln(out, "Note: github.scope is not workspace for this project, so runs are not scoped yet.")
	}
	return nil
}

func githubForget(cmd *cobra.Command, args []string) error {
	if githubForgetAll == (len(args) > 0) {
		return fmt.Errorf("name the repositories to forget, or pass --all to reset the project")
	}
	p, err := openGitHubProject()
	if err != nil {
		return err
	}
	defer p.store.Close()
	out := cmd.OutOrStdout()

	if githubForgetAll {
		existed, resetErr := githubscope.Reset(p.store, p.dir)
		if resetErr != nil {
			return resetErr
		}
		if !existed {
			fmt.Fprintf(out, "No GitHub approvals recorded for %s\n", p.dir)
			return nil
		}
		fmt.Fprintf(out, "%s Forgot all GitHub approvals for %s and unlocked it\n", ui.OKTag(), p.dir)
		if p.settings.Enabled() {
			fmt.Fprintln(out, "moat.yaml still sets github.scope: workspace, so the next run approves the repositories it finds.")
		}
		return nil
	}

	repos := make([]string, 0, len(args))
	for _, a := range args {
		repo, normErr := githubscope.NormalizeRepo(a)
		if normErr != nil {
			return normErr
		}
		repos = append(repos, repo)
	}
	notFound, err := githubscope.Forget(p.store, p.dir, repos)
	if err != nil {
		return err
	}
	missing := map[string]bool{}
	for _, r := range notFound {
		missing[r] = true
	}
	for _, r := range repos {
		if missing[r] {
			fmt.Fprintf(out, "%s was not approved for %s\n", r, p.dir)
			continue
		}
		fmt.Fprintf(out, "%s Runs in %s can no longer push to %s\n", ui.OKTag(), p.dir, r)
	}
	return nil
}

func githubPolicy(cmd *cobra.Command, _ []string) error {
	p, err := openGitHubProject()
	if err != nil {
		return err
	}
	defer p.store.Close()
	plan, existing, err := p.plan()
	if err != nil {
		return err
	}
	policy, err := githubscope.RenderPolicy(githubscope.PlanPolicyParams(plan, existing, nil))
	if err != nil {
		return err
	}
	_, err = cmd.OutOrStdout().Write(policy)
	return err
}
