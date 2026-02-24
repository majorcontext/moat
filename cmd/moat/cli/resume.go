package cli

import (
	"context"
	"fmt"
	"sort"

	intcli "github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/log"
	claudeprovider "github.com/majorcontext/moat/internal/providers/claude"
	"github.com/majorcontext/moat/internal/run"
	"github.com/spf13/cobra"
)

var (
	resumeFlags  ExecFlags
	resumeNoYolo bool
)

var resumeCmd = &cobra.Command{
	Use:   "resume [run]",
	Short: "Resume a Claude Code conversation",
	Long: `Resume a Claude Code conversation from a previous run.

If the specified run is still running, attaches to it (same as 'moat attach').
If the run has stopped, starts a new container with the same workspace and
passes --continue to Claude Code, which picks up the most recent conversation
from the synced session logs.

Without an argument, finds the most recent Claude Code run (running or stopped)
and resumes it.

Session history is preserved across runs because moat mounts the Claude
projects directory (~/.claude/projects/) between host and container. Claude
Code stores conversation logs as .jsonl files in this directory, so previous
sessions are always available for resumption.

Examples:
  # Resume the most recent Claude Code run
  moat resume

  # Resume a specific run by name
  moat resume my-feature

  # Resume a specific run by ID
  moat resume run_a1b2c3d4e5f6

  # Resume with additional grants
  moat resume my-feature --grant github`,
	Args: cobra.MaximumNArgs(1),
	RunE: runResume,
}

func init() {
	rootCmd.AddCommand(resumeCmd)
	AddExecFlags(resumeCmd, &resumeFlags)
	resumeCmd.Flags().BoolVar(&resumeNoYolo, "noyolo", false, "disable --dangerously-skip-permissions")
}

func runResume(cmd *cobra.Command, args []string) error {
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	var target *run.Run
	if len(args) > 0 {
		// Resolve by name or ID
		runID, resolveErr := resolveRunArgSingle(manager, args[0])
		if resolveErr != nil {
			return resolveErr
		}
		target, err = manager.Get(runID)
		if err != nil {
			return fmt.Errorf("run not found: %w", err)
		}
	} else {
		// Find the most recent Claude Code run (running preferred, then stopped)
		target = findMostRecentClaudeRun(manager)
		if target == nil {
			return fmt.Errorf("no Claude Code runs found\n\nStart a session first with: moat claude")
		}
	}

	// Verify this is a Claude Code run
	if !isClaudeRun(target) {
		return fmt.Errorf("run %s uses agent %q, but resume is only supported for Claude Code runs", target.ID[:8], target.Agent)
	}

	// If running, attach to it
	if target.State == run.StateRunning {
		fmt.Printf("Run %s (%s) is still running, attaching...\n", target.Name, target.ID)
		ctx := context.Background()
		return attachInteractiveMode(ctx, manager, target, "")
	}

	// If stopped, start a new container with the same workspace
	if target.State != run.StateStopped && target.State != run.StateFailed {
		if target.State == run.StateCreated || target.State == run.StateStarting {
			return fmt.Errorf("run %s is still starting up; use 'moat attach %s' once it is running", target.ID[:8], target.Name)
		}
		return fmt.Errorf("run %s is in state %q and cannot be resumed", target.ID[:8], target.State)
	}

	return resumeStoppedRun(target)
}

// findMostRecentClaudeRun finds the most recent Claude Code run, preferring running ones.
func findMostRecentClaudeRun(manager *run.Manager) *run.Run {
	return selectResumableRun(manager.List())
}

// selectResumableRun picks the best Claude run to resume from a list.
// Running runs are preferred over stopped/failed; within each tier the
// most recently created run wins. Non-Claude and still-starting runs
// are excluded.
func selectResumableRun(runs []*run.Run) *run.Run {
	if len(runs) == 0 {
		return nil
	}

	// Sort newest first
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})

	// First pass: find most recent running Claude run
	for _, r := range runs {
		if r.State == run.StateRunning && isClaudeRun(r) {
			return r
		}
	}

	// Second pass: find most recent stopped/failed Claude run
	for _, r := range runs {
		if (r.State == run.StateStopped || r.State == run.StateFailed) && isClaudeRun(r) {
			return r
		}
	}

	return nil
}

// isClaudeRun returns true if the run was a Claude Code session.
func isClaudeRun(r *run.Run) bool {
	return r.Agent == "claude-code" || r.Agent == "claude"
}

// resumeStoppedRun starts a new container with the same workspace and --continue.
func resumeStoppedRun(prev *run.Run) error {
	absPath := prev.Workspace

	fmt.Printf("Resuming Claude Code session from run %s (%s)\n", prev.Name, prev.ID)

	// Load agent.yaml if present
	cfg, err := config.Load(absPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Build grants list: previous run's grants + config grants + flag grants
	grantSet := make(map[string]bool)
	var grants []string
	addGrant := func(g string) {
		if !grantSet[g] {
			grantSet[g] = true
			grants = append(grants, g)
		}
	}

	// Start with the previous run's grants
	for _, g := range prev.Grants {
		addGrant(g)
	}

	// Auto-detect credential if previous run had none
	if len(prev.Grants) == 0 {
		if credName := claudeprovider.GetClaudeCredentialName(); credName != "" {
			addGrant(credName)
		}
	}

	// Add config grants
	if cfg != nil {
		for _, g := range cfg.Grants {
			addGrant(g)
		}
	}

	// Add flag grants
	for _, g := range resumeFlags.Grants {
		addGrant(g)
	}
	resumeFlags.Grants = grants

	// Build container command with --continue
	containerCmd := []string{"claude"}
	if !resumeNoYolo {
		containerCmd = append(containerCmd, "--dangerously-skip-permissions")
	}
	containerCmd = append(containerCmd, "--continue")

	// Use name from flag, or previous run's name
	if resumeFlags.Name == "" {
		resumeFlags.Name = prev.Name
	}

	// Ensure dependencies
	if cfg == nil {
		cfg = &config.Config{}
	}
	if !intcli.HasDependency(cfg.Dependencies, "node") {
		cfg.Dependencies = append(cfg.Dependencies, "node@20")
	}
	if !intcli.HasDependency(cfg.Dependencies, "git") {
		cfg.Dependencies = append(cfg.Dependencies, "git")
	}
	if !intcli.HasDependency(cfg.Dependencies, "claude-code") {
		cfg.Dependencies = append(cfg.Dependencies, "claude-code")
	}

	// Always sync Claude logs (required for resume to work)
	syncLogs := true
	cfg.Claude.SyncLogs = &syncLogs

	// Allow network access to claude.ai
	cfg.Network.Allow = append(cfg.Network.Allow, "claude.ai", "*.claude.ai")

	// Add environment variables from flags
	if envErr := intcli.ParseEnvFlags(resumeFlags.Env, cfg); envErr != nil {
		return envErr
	}

	log.Debug("resuming claude code session",
		"workspace", absPath,
		"previous_run", prev.ID,
		"grants", grants,
	)

	if intcli.DryRun {
		fmt.Println("Dry run - would resume Claude Code session")
		fmt.Printf("Previous run: %s (%s)\n", prev.Name, prev.ID)
		fmt.Printf("Workspace: %s\n", absPath)
		fmt.Printf("Grants: %v\n", grants)
		return nil
	}

	ctx := context.Background()

	opts := intcli.ExecOptions{
		Flags:       resumeFlags,
		Workspace:   absPath,
		Command:     containerCmd,
		Config:      cfg,
		Interactive: true,
		TTY:         true,
	}

	if !resumeFlags.Detach {
		fmt.Printf("Resuming Claude Code in %s\n", absPath)
	}

	_, err = intcli.ExecuteRun(ctx, opts)
	if err != nil {
		return err
	}

	return nil
}
