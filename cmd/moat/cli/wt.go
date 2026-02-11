package cli

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	intcli "github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/ui"
	"github.com/majorcontext/moat/internal/worktree"
)

var wtFlags intcli.ExecFlags

var wtCmd = &cobra.Command{
	Use:   "wt <branch> [-- command]",
	Short: "Run agent in a git worktree",
	Long: `Create or reuse a git worktree for a branch and run an agent in it.

If the branch doesn't exist, it's created from HEAD. If the worktree doesn't
exist, it's created. If a run is already active in the worktree, returns an
error with instructions to attach or stop it.

Agent configuration is read from agent.yaml in the repository root.

Worktrees are stored at ~/.moat/worktrees/<repo-id>/<branch>.
Override with MOAT_WORKTREE_BASE environment variable.

Examples:
  # Start agent on a new feature branch
  moat wt dark-mode

  # Start agent in background
  moat wt dark-mode -d

  # Start with a specific command
  moat wt dark-mode -- make test

  # List worktree-based runs
  moat wt list

  # Clean up stopped worktrees
  moat wt clean
  moat wt clean dark-mode`,
	Args: cobra.ArbitraryArgs,
	RunE: runWorktree,
}

func init() {
	rootCmd.AddCommand(wtCmd)
	intcli.AddExecFlags(wtCmd, &wtFlags)

	wtListCmd := &cobra.Command{
		Use:   "list",
		Short: "List worktree-based runs",
		RunE:  runWorktreeList,
	}
	wtCmd.AddCommand(wtListCmd)

	wtCleanCmd := &cobra.Command{
		Use:   "clean [branch]",
		Short: "Remove worktree directories for stopped runs",
		Long: `Remove worktree directories for stopped runs. Never deletes branches.

Without arguments, cleans all worktrees for the current repo whose runs are stopped.
With a branch name, cleans only that worktree.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runWorktreeClean,
	}
	wtCmd.AddCommand(wtCleanCmd)
}

func runWorktree(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("branch name required\n\nUsage: moat wt <branch> [-- command]")
	}

	branch := args[0]

	// Parse command after --
	var containerCmd []string
	dashIdx := cmd.ArgsLenAtDash()
	if dashIdx >= 0 {
		containerCmd = args[dashIdx:]
	}

	// Find git repo root
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting current directory: %w", err)
	}

	repoRoot, err := worktree.FindRepoRoot(cwd)
	if err != nil {
		return fmt.Errorf("not inside a git repository: %w", err)
	}

	// Load agent.yaml from repo root
	cfg, err := config.Load(repoRoot)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg == nil {
		return fmt.Errorf("agent.yaml not found in %s\n\nmoat wt requires an agent.yaml to determine which agent to run.\nSee https://majorcontext.com/moat/reference/agent-yaml", repoRoot)
	}

	// Resolve repo ID
	repoID, err := worktree.ResolveRepoID(repoRoot)
	if err != nil {
		return fmt.Errorf("resolving repo identity: %w", err)
	}

	// Resolve worktree
	result, err := worktree.Resolve(repoRoot, repoID, branch, cfg.Name)
	if err != nil {
		return fmt.Errorf("resolving worktree: %w", err)
	}

	// User feedback
	if result.Reused {
		ui.Infof("Using existing worktree at %s", result.WorkspacePath)
	} else {
		ui.Infof("Created worktree at %s", result.WorkspacePath)
	}

	// Reload config from worktree if it has its own agent.yaml
	if wtCfg, loadErr := config.Load(result.WorkspacePath); loadErr == nil && wtCfg != nil {
		cfg = wtCfg
	}

	// Check for active run in this worktree
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	for _, r := range manager.List() {
		if r.WorktreePath == result.WorkspacePath && r.State == run.StateRunning {
			return fmt.Errorf("a run is already active in worktree for branch %q: %s (%s)\nAttach with 'moat attach %s' or stop with 'moat stop %s'", branch, r.Name, r.ID, r.ID, r.ID)
		}
	}

	// Use run name from worktree resolution unless --name overrides
	if wtFlags.Name == "" {
		wtFlags.Name = result.RunName
	}

	// Apply config defaults (same pattern as moat run)
	if cfg != nil {
		if len(wtFlags.Grants) == 0 && len(cfg.Grants) > 0 {
			wtFlags.Grants = cfg.Grants
		}
		if len(containerCmd) == 0 && len(cfg.Command) > 0 {
			containerCmd = cfg.Command
		}
		if cfg.Sandbox == "none" && !wtFlags.NoSandbox {
			wtFlags.NoSandbox = true
		}
	}

	// Determine interactive mode: CLI flags > config > default
	interactive := !wtFlags.Detach
	if !interactive && cfg != nil && cfg.Interactive {
		interactive = true
	}

	log.Debug("starting worktree run",
		"branch", branch,
		"workspace", result.WorkspacePath,
		"run_name", wtFlags.Name,
		"grants", wtFlags.Grants,
	)

	if dryRun {
		fmt.Printf("Dry run - would start agent in worktree\n")
		fmt.Printf("Branch: %s\n", branch)
		fmt.Printf("Workspace: %s\n", result.WorkspacePath)
		fmt.Printf("Run name: %s\n", wtFlags.Name)
		fmt.Printf("Grants: %v\n", wtFlags.Grants)
		return nil
	}

	ctx := cmd.Context()

	opts := intcli.ExecOptions{
		Flags:       wtFlags,
		Workspace:   result.WorkspacePath,
		Command:     containerCmd,
		Config:      cfg,
		Interactive: interactive,
		TTY:         interactive,
	}
	intcli.SetWorktreeFields(&opts, result)

	_, err = ExecuteRun(ctx, opts)
	return err
}

func runWorktreeList(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting current directory: %w", err)
	}

	repoRoot, err := worktree.FindRepoRoot(cwd)
	if err != nil {
		return fmt.Errorf("not inside a git repository: %w", err)
	}

	repoID, err := worktree.ResolveRepoID(repoRoot)
	if err != nil {
		return fmt.Errorf("resolving repo identity: %w", err)
	}

	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	runs := manager.List()

	var wtRuns []*run.Run
	for _, r := range runs {
		if r.WorktreeRepoID == repoID {
			wtRuns = append(wtRuns, r)
		}
	}

	if len(wtRuns) == 0 {
		fmt.Println("No worktree runs found for this repository")
		return nil
	}

	sort.Slice(wtRuns, func(i, j int) bool {
		return wtRuns[i].CreatedAt.After(wtRuns[j].CreatedAt)
	})

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "BRANCH\tRUN NAME\tSTATUS\tWORKTREE")
	for _, r := range wtRuns {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			r.WorktreeBranch, r.Name, r.State, intcli.ShortenPath(r.WorktreePath))
	}
	return w.Flush()
}

func runWorktreeClean(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting current directory: %w", err)
	}

	repoRoot, err := worktree.FindRepoRoot(cwd)
	if err != nil {
		return fmt.Errorf("not inside a git repository: %w", err)
	}

	repoID, err := worktree.ResolveRepoID(repoRoot)
	if err != nil {
		return fmt.Errorf("resolving repo identity: %w", err)
	}

	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	if len(args) > 0 {
		branch := args[0]
		wtPath := worktree.Path(repoID, branch)

		for _, r := range manager.List() {
			if r.WorktreePath == wtPath && r.State == run.StateRunning {
				return fmt.Errorf("cannot clean worktree for branch %q: run %s is still active. Stop it first with 'moat stop %s'", branch, r.Name, r.ID)
			}
		}

		if cleanErr := worktree.Clean(repoRoot, wtPath); cleanErr != nil {
			return cleanErr
		}
		ui.Infof("Cleaned worktree for branch %s", branch)
		return nil
	}

	entries, err := worktree.ListWorktrees(repoID)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		fmt.Println("No worktrees to clean")
		return nil
	}

	cleaned := 0
	for _, entry := range entries {
		active := false
		for _, r := range manager.List() {
			if r.WorktreePath == entry.Path && r.State == run.StateRunning {
				active = true
				break
			}
		}
		if active {
			continue
		}

		if err := worktree.Clean(repoRoot, entry.Path); err != nil {
			ui.Warnf("Failed to clean %s: %v", entry.Branch, err)
			continue
		}
		ui.Infof("Cleaned worktree for branch %s", entry.Branch)
		cleaned++
	}

	if cleaned == 0 {
		fmt.Println("No stopped worktrees to clean")
	}
	return nil
}
