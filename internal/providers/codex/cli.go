package codex

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/worktree"
)

var (
	codexFlags        cli.ExecFlags
	codexPromptFlag   string
	codexAllowedHosts []string
	codexFullAuto     bool
	codexWtFlag       string
)

// NetworkHosts returns the list of hosts that Codex needs network access to.
// These should be added to the network allow list for containers running Codex.
func NetworkHosts() []string {
	return []string{
		"api.openai.com",
		"*.openai.com",
		"auth.openai.com",
		"platform.openai.com",
		"chatgpt.com",
		"*.chatgpt.com",
	}
}

// DefaultDependencies returns the default dependencies for running Codex CLI.
func DefaultDependencies() []string {
	return []string{
		"node@20",
		"git",
		"codex-cli",
	}
}

// RegisterCLI registers Codex-related CLI commands.
// This adds the `moat codex` command group with subcommands.
func (p *Provider) RegisterCLI(root *cobra.Command) {
	codexCmd := &cobra.Command{
		Use:   "codex [workspace] [flags]",
		Short: "Run Codex CLI in an isolated container",
		Long: `Run OpenAI Codex CLI in an isolated container with automatic credential injection.

Your workspace is mounted at /workspace inside the container. API credentials
are injected transparently via the Moat proxy - Codex CLI never sees raw tokens.

By default, Codex runs with --full-auto mode enabled (auto-approves tool use).
Use --full-auto=false to require manual approval for each action.

Without a workspace argument, uses the current directory.

Examples:
  # Start Codex CLI in current directory (interactive)
  moat codex

  # Start Codex CLI in a specific project
  moat codex ./my-project

  # Ask Codex to do something specific (non-interactive)
  moat codex -p "explain this codebase"
  moat codex -p "fix the bug in main.py"

  # Add additional grants (e.g., for GitHub API access)
  moat codex --grant github

  # Name the session for easy reference
  moat codex --name my-feature

  # Run in background
  moat codex -d

  # Force rebuild of container image
  moat codex --rebuild

  # Disable full-auto mode (require manual approval)
  moat codex --full-auto=false

Use 'moat list' to see running and recent runs.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runCodex,
	}

	// Add shared execution flags
	cli.AddExecFlags(codexCmd, &codexFlags)

	// Add Codex-specific flags
	codexCmd.Flags().StringVarP(&codexPromptFlag, "prompt", "p", "", "run with prompt (non-interactive mode)")
	codexCmd.Flags().StringSliceVar(&codexAllowedHosts, "allow-host", nil, "additional hosts to allow network access to")
	codexCmd.Flags().BoolVar(&codexFullAuto, "full-auto", true, "enable full-auto mode (auto-approve tool use); set to false for manual approval")
	codexCmd.Flags().StringVar(&codexWtFlag, "wt", "", "run in a git worktree for this branch")

	root.AddCommand(codexCmd)
}

func runCodex(cmd *cobra.Command, args []string) error {
	// If subcommand is being run, don't execute this
	if cmd.CalledAs() != "codex" {
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
	absPath, wtResult, err = cli.ResolveWorktreeWorkspace(codexWtFlag, absPath, &codexFlags, cfg)
	if err != nil {
		return err
	}
	if wtResult != nil {
		if wtResult.Reused {
			fmt.Fprintf(os.Stderr, "Using existing worktree at %s\n", wtResult.WorkspacePath)
		} else {
			fmt.Fprintf(os.Stderr, "Created worktree at %s\n", wtResult.WorkspacePath)
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

	if credName := getCodexCredentialName(); credName != "" {
		addGrant(credName) // Use the actual name the credential is stored under
	}
	if cfg != nil {
		for _, g := range cfg.Grants {
			addGrant(g)
		}
	}
	for _, g := range codexFlags.Grants {
		addGrant(g)
	}
	codexFlags.Grants = grants

	// Determine interactive mode
	interactive := codexPromptFlag == ""

	// Build container command
	// codex is installed globally via the dependency system
	var containerCmd []string

	if codexPromptFlag != "" {
		// Non-interactive mode: use `codex exec` with the prompt
		// --full-auto allows edits during execution (safe since we're in a container)
		containerCmd = []string{"codex", "exec"}
		if codexFullAuto {
			containerCmd = append(containerCmd, "--full-auto")
		}
		containerCmd = append(containerCmd, codexPromptFlag)
	} else {
		// Interactive mode: just run `codex` for the TUI
		containerCmd = []string{"codex"}
	}

	// Use name from flag, or config, or let manager generate one
	if codexFlags.Name == "" && cfg != nil && cfg.Name != "" {
		codexFlags.Name = cfg.Name
	}

	// Ensure dependencies for Codex CLI
	if cfg == nil {
		cfg = &config.Config{}
	}
	if !cli.HasDependency(cfg.Dependencies, "node") {
		cfg.Dependencies = append(cfg.Dependencies, "node@20")
	}
	if !cli.HasDependency(cfg.Dependencies, "git") {
		cfg.Dependencies = append(cfg.Dependencies, "git")
	}
	if !cli.HasDependency(cfg.Dependencies, "codex-cli") {
		cfg.Dependencies = append(cfg.Dependencies, "codex-cli")
	}

	// Allow network access to OpenAI
	cfg.Network.Allow = append(cfg.Network.Allow, NetworkHosts()...)

	// Add allowed hosts if specified
	cfg.Network.Allow = append(cfg.Network.Allow, codexAllowedHosts...)

	// Always sync Codex logs
	syncLogs := true
	cfg.Codex.SyncLogs = &syncLogs

	// Add environment variables from flags
	if envErr := cli.ParseEnvFlags(codexFlags.Env, cfg); envErr != nil {
		return envErr
	}

	log.Debug("starting codex cli",
		"workspace", absPath,
		"grants", grants,
		"interactive", interactive,
		"prompt", codexPromptFlag,
		"rebuild", codexFlags.Rebuild,
	)

	if cli.DryRun {
		fmt.Println("Dry run - would start Codex CLI")
		fmt.Printf("Workspace: %s\n", absPath)
		fmt.Printf("Grants: %v\n", grants)
		fmt.Printf("Interactive: %v\n", interactive)
		fmt.Printf("Rebuild: %v\n", codexFlags.Rebuild)
		if len(grants) == 0 {
			fmt.Println("Note: No API key configured. Codex will prompt for login.")
		}
		return nil
	}

	ctx := context.Background()

	opts := cli.ExecOptions{
		Flags:       codexFlags,
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

	if result != nil && !codexFlags.Detach {
		fmt.Printf("Starting Codex CLI in %s\n", absPath)
		fmt.Printf("Run: %s (%s)\n", result.Name, result.ID)
	}

	return nil
}

// getCodexCredentialName returns the name under which the Codex credential is stored.
// Returns empty string if no credential exists.
func getCodexCredentialName() string {
	// Check both provider names (codex is the internal name, openai is legacy)
	for _, name := range []string{"codex", "openai"} {
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
