package claude

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/ui"
	"github.com/majorcontext/moat/internal/worktree"
)

var (
	claudeFlags        cli.ExecFlags
	claudePromptFlag   string
	claudeAllowedHosts []string
	claudeNoYolo       bool
	claudeWtFlag       string
)

// RegisterCLI adds provider-specific commands to the root command.
// This adds the `moat claude` command group with subcommands.
func (p *Provider) RegisterCLI(root *cobra.Command) {
	claudeCmd := &cobra.Command{
		Use:   "claude [workspace] [flags]",
		Short: "Run Claude Code in an isolated container",
		Long: `Run Claude Code in an isolated container with automatic credential injection.

Your workspace is mounted at /workspace inside the container. API credentials
are injected transparently via the Moat proxy - Claude Code never sees raw tokens.

By default, Claude runs with --dangerously-skip-permissions since the container
provides isolation. Use --noyolo to require manual approval for each tool use.

Without a workspace argument, uses the current directory.

Examples:
  # Start Claude Code in current directory (interactive)
  moat claude

  # Start Claude Code in a specific project
  moat claude ./my-project

  # Ask Claude to do something specific (non-interactive)
  moat claude -p "explain this codebase"
  moat claude -p "fix the bug in main.py"

  # Add additional grants (e.g., for GitHub API access)
  moat claude --grant github

  # Name the session for easy reference
  moat claude --name my-feature

  # Run in background
  moat claude -d

  # Force rebuild of container image
  moat claude --rebuild

  # Require manual approval for each tool use (disable yolo mode)
  moat claude --noyolo

Use 'moat list' to see running and recent runs.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runClaudeCode,
	}

	// Add shared execution flags
	cli.AddExecFlags(claudeCmd, &claudeFlags)

	// Add Claude-specific flags
	claudeCmd.Flags().StringVarP(&claudePromptFlag, "prompt", "p", "", "run with prompt (non-interactive mode)")
	claudeCmd.Flags().StringSliceVar(&claudeAllowedHosts, "allow-host", nil, "additional hosts to allow network access to")
	claudeCmd.Flags().BoolVar(&claudeNoYolo, "noyolo", false, "disable --dangerously-skip-permissions (require manual approval for each tool use)")
	claudeCmd.Flags().StringVar(&claudeWtFlag, "wt", "", "run in a git worktree for this branch")

	root.AddCommand(claudeCmd)
}

func runClaudeCode(cmd *cobra.Command, args []string) error {
	// If subcommand is being run, don't execute this
	if cmd.CalledAs() != "claude" {
		return nil
	}

	// Determine workspace
	workspace := "."
	if len(args) > 0 {
		workspace = args[0]
	}

	absPath, err := cli.ResolveWorkspacePath(workspace)
	if err != nil {
		return err
	}

	// Load agent.yaml if present, otherwise use defaults
	cfg, err := config.Load(absPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Handle --wt flag
	var wtResult *worktree.Result
	absPath, wtResult, err = cli.ResolveWorktreeWorkspace(claudeWtFlag, absPath, &claudeFlags, cfg)
	if err != nil {
		return err
	}
	if wtResult != nil {
		if wtResult.Reused {
			ui.Infof("Using existing worktree at %s", wtResult.WorkspacePath)
		} else {
			ui.Infof("Created worktree at %s", wtResult.WorkspacePath)
		}
		// Reload config from worktree path if it has its own agent.yaml
		if wtCfg, loadErr := config.Load(absPath); loadErr == nil && wtCfg != nil {
			cfg = wtCfg
		}
	}

	// Build grants list using a set for deduplication
	grantSet := make(map[string]bool)
	var grants []string
	addGrant := func(g string) {
		if !grantSet[g] {
			grantSet[g] = true
			grants = append(grants, g)
		}
	}

	if credName := getClaudeCredentialName(); credName != "" {
		addGrant(credName) // Use the actual name the credential is stored under
	}
	if cfg != nil {
		for _, g := range cfg.Grants {
			addGrant(g)
		}
	}
	for _, g := range claudeFlags.Grants {
		addGrant(g)
	}
	claudeFlags.Grants = grants

	// Determine interactive mode
	interactive := claudePromptFlag == ""

	// Build container command
	containerCmd := []string{"claude"}

	// By default, skip permission prompts since Moat provides isolation.
	if !claudeNoYolo {
		containerCmd = append(containerCmd, "--dangerously-skip-permissions")
	}

	if claudePromptFlag != "" {
		containerCmd = append(containerCmd, "-p", claudePromptFlag)
	}

	// Use name from flag, or config, or let manager generate one
	if claudeFlags.Name == "" && cfg != nil && cfg.Name != "" {
		claudeFlags.Name = cfg.Name
	}

	// Ensure dependencies for Claude Code
	if cfg == nil {
		cfg = &config.Config{}
	}
	if !cli.HasDependency(cfg.Dependencies, "node") {
		cfg.Dependencies = append(cfg.Dependencies, "node@20")
	}
	if !cli.HasDependency(cfg.Dependencies, "git") {
		cfg.Dependencies = append(cfg.Dependencies, "git")
	}
	if !cli.HasDependency(cfg.Dependencies, "claude-code") {
		cfg.Dependencies = append(cfg.Dependencies, "claude-code")
	}

	// Always sync Claude logs
	syncLogs := true
	cfg.Claude.SyncLogs = &syncLogs

	// Allow network access to claude.ai for OAuth login
	cfg.Network.Allow = append(cfg.Network.Allow, "claude.ai", "*.claude.ai")

	// Add allowed hosts if specified
	cfg.Network.Allow = append(cfg.Network.Allow, claudeAllowedHosts...)

	// Add environment variables from flags
	if envErr := cli.ParseEnvFlags(claudeFlags.Env, cfg); envErr != nil {
		return envErr
	}

	log.Debug("starting claude code",
		"workspace", absPath,
		"grants", grants,
		"interactive", interactive,
		"prompt", claudePromptFlag,
		"rebuild", claudeFlags.Rebuild,
	)

	if cli.DryRun {
		fmt.Println("Dry run - would start Claude Code")
		fmt.Printf("Workspace: %s\n", absPath)
		fmt.Printf("Grants: %v\n", grants)
		fmt.Printf("Interactive: %v\n", interactive)
		fmt.Printf("Rebuild: %v\n", claudeFlags.Rebuild)
		if len(grants) == 0 {
			fmt.Println("Note: No API key configured. Claude will prompt for login.")
		}
		return nil
	}

	ctx := context.Background()

	opts := cli.ExecOptions{
		Flags:       claudeFlags,
		Workspace:   absPath,
		Command:     containerCmd,
		Config:      cfg,
		Interactive: interactive,
		TTY:         interactive,
	}

	if wtResult != nil {
		opts.WorktreeBranch = wtResult.Branch
		opts.WorktreePath = wtResult.WorkspacePath
		opts.WorktreeRepoID = wtResult.RepoID
	}

	result, err := cli.ExecuteRun(ctx, opts)
	if err != nil {
		return err
	}

	if result != nil && !claudeFlags.Detach {
		fmt.Printf("Starting Claude Code in %s\n", absPath)
		fmt.Printf("Run: %s (%s)\n", result.Name, result.ID)
	}

	return nil
}

// getClaudeCredentialName returns the name under which the Claude credential is stored.
// Returns empty string if no credential exists.
func getClaudeCredentialName() string {
	// Check both provider names (claude is the internal name, anthropic is legacy)
	for _, name := range []string{"claude", "anthropic"} {
		key, err := credential.DefaultEncryptionKey()
		if err != nil {
			continue
		}
		store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
		if err != nil {
			continue
		}
		if _, err := store.Get(credential.Provider(name)); err == nil {
			return name
		}
	}
	return ""
}
