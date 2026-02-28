package claude

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/storage"
)

var (
	claudeFlags        cli.ExecFlags
	claudePromptFlag   string
	claudeAllowedHosts []string
	claudeNoYolo       bool
	claudeWtFlag       string
	claudeContinue     bool
	claudeResume       string
)

// RegisterCLI adds provider-specific commands to the root command.
// This adds the `moat claude` command group with subcommands.
func (p *OAuthProvider) RegisterCLI(root *cobra.Command) {
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

  # Start with an initial prompt (interactive - Claude stays open)
  moat claude -- "is this thing on?"
  moat claude ./my-project -- "explain this codebase"

  # Ask Claude to do something specific (non-interactive - exits when done)
  moat claude -p "explain this codebase"
  moat claude -p "fix the bug in main.py"

  # Add additional grants (e.g., for GitHub API access)
  moat claude --grant github

  # Name the session for easy reference
  moat claude --name my-feature

  # Force rebuild of container image
  moat claude --rebuild

  # Require manual approval for each tool use (disable yolo mode)
  moat claude --noyolo

  # Continue the most recent conversation
  moat claude --continue
  moat claude -c

  # Resume a specific session by ID
  moat claude --resume ae150251-d90a-4f85-a9da-2281e8e0518d

Use 'moat list' to see running and recent runs.`,
		Args: cobra.ArbitraryArgs,
		RunE: runClaudeCode,
	}

	// Add shared execution flags
	cli.AddExecFlags(claudeCmd, &claudeFlags)

	// Add Claude-specific flags
	claudeCmd.Flags().StringVarP(&claudePromptFlag, "prompt", "p", "", "run with prompt (non-interactive mode)")
	claudeCmd.Flags().StringSliceVar(&claudeAllowedHosts, "allow-host", nil, "additional hosts to allow network access to")
	claudeCmd.Flags().BoolVar(&claudeNoYolo, "noyolo", false, "disable --dangerously-skip-permissions (require manual approval for each tool use)")
	claudeCmd.Flags().BoolVarP(&claudeContinue, "continue", "c", false, "continue the most recent conversation")
	claudeCmd.Flags().StringVarP(&claudeResume, "resume", "r", "", "resume a specific session by ID")
	claudeCmd.Flags().StringVar(&claudeWtFlag, "worktree", "", "run in a git worktree for this branch")
	claudeCmd.Flags().StringVar(&claudeWtFlag, "wt", "", "alias for --worktree")
	_ = claudeCmd.Flags().MarkHidden("wt")

	root.AddCommand(claudeCmd)
}

func runClaudeCode(cmd *cobra.Command, args []string) error {
	// If subcommand is being run, don't execute this
	if cmd.CalledAs() != "claude" {
		return nil
	}

	// Separate workspace arg from initial prompt args.
	// Everything after "--" is passed as an initial prompt to Claude.
	//   moat claude -- "is this thing on?"          → workspace=".", prompt="is this thing on?"
	//   moat claude ./project -- "explain this"     → workspace="./project", prompt="explain this"
	//   moat claude ./project                       → workspace="./project", no prompt
	var initialPrompt string
	workspace := "."
	dashIdx := cmd.ArgsLenAtDash()
	if dashIdx >= 0 {
		// Args before "--" are moat args (workspace), after are passthrough
		if dashIdx > 0 {
			workspace = args[0]
		}
		passthroughArgs := args[dashIdx:]
		if len(passthroughArgs) > 0 {
			initialPrompt = strings.Join(passthroughArgs, " ")
		}
	} else if len(args) > 0 {
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
	wtOut, err := cli.ResolveWorktreeWorkspace(claudeWtFlag, absPath, &claudeFlags, cfg)
	if err != nil {
		return err
	}
	absPath = wtOut.Workspace
	cfg = wtOut.Config

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

	// Validate mutually exclusive flags
	if claudeContinue && claudeResume != "" {
		return fmt.Errorf("--continue and --resume are mutually exclusive")
	}

	// Build container command
	containerCmd := []string{"claude"}

	// By default, skip permission prompts since Moat provides isolation.
	if !claudeNoYolo {
		containerCmd = append(containerCmd, "--dangerously-skip-permissions")
	}

	if claudeContinue {
		containerCmd = append(containerCmd, "--continue")
	}

	if claudeResume != "" {
		sessionID, resolveErr := resolveResumeSession(claudeResume)
		if resolveErr != nil {
			return resolveErr
		}
		containerCmd = append(containerCmd, "--resume", sessionID)
	}

	if claudePromptFlag != "" {
		containerCmd = append(containerCmd, "-p", claudePromptFlag)
	}

	if initialPrompt != "" {
		containerCmd = append(containerCmd, initialPrompt)
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
	}

	cli.SetWorktreeFields(&opts, wtOut.Result)

	result, err := cli.ExecuteRun(ctx, opts)
	if err != nil {
		return err
	}

	if result != nil {
		fmt.Printf("Starting Claude Code in %s\n", absPath)
		fmt.Printf("Run: %s (%s)\n", result.Name, result.ID)
	}

	return nil
}

// getClaudeCredentialName returns the grant name to use for moat claude.
//
// Preference order:
//  1. claude (OAuth token) — preferred for Claude Code
//  2. anthropic (API key) — fallback, works with Claude Code too
//
// Auto-migration (runs once, on first moat claude after upgrade):
//   - claude-oauth.enc (old name) → claude.enc
//   - anthropic.enc with OAuth token → claude.enc
//
// Returns empty string if no credential exists.
func getClaudeCredentialName() string {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return ""
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return ""
	}

	return resolveClaudeCredential(store)
}

// resolveClaudeCredential checks the credential store for a Claude-compatible
// credential. It performs one-time migrations from legacy credential names
// (claude-oauth, or OAuth tokens stored under anthropic) to the canonical
// "claude" provider slot.
//
// This function mutates the store when migration is needed. It is safe to call
// multiple times — once migrated, subsequent calls hit the fast path.
func resolveClaudeCredential(store *credential.FileStore) string {
	// Fast path: claude credential already exists
	if _, getErr := store.Get(credential.ProviderClaude); getErr == nil {
		return "claude"
	}

	// Auto-migrate: claude-oauth.enc (old provider name) → claude.enc
	if oldCred, getErr := store.Get("claude-oauth"); getErr == nil {
		migrated := *oldCred
		migrated.Provider = credential.ProviderClaude
		if saveErr := store.Save(migrated); saveErr == nil {
			_ = store.Delete("claude-oauth")
			log.Info("migrated credential from claude-oauth to claude",
				"subsystem", "grant",
			)
			return "claude"
		}
	}

	// Check anthropic
	cred, getErr := store.Get(credential.ProviderAnthropic)
	if getErr != nil {
		return ""
	}

	// Auto-migrate: if anthropic.enc contains an OAuth token, move it to claude.enc
	if credential.IsOAuthToken(cred.Token) {
		migrated := *cred
		migrated.Provider = credential.ProviderClaude
		if saveErr := store.Save(migrated); saveErr == nil {
			_ = store.Delete(credential.ProviderAnthropic)
			log.Info("migrated OAuth token from anthropic to claude",
				"subsystem", "grant",
			)
			return "claude"
		}
		// Migration failed — fall through to use anthropic as-is
	}

	return "anthropic"
}

// resolveResumeSession resolves a --resume argument to a Claude session UUID.
//
// If the argument is already a UUID, it is returned as-is (backward compatible).
// Otherwise, we treat it as a moat run name or ID, look up the run's stored
// ClaudeSessionID from metadata, and return that.
func resolveResumeSession(arg string) (string, error) {
	return resolveResumeSessionInDir(arg, storage.DefaultBaseDir())
}

// resolveResumeSessionInDir is the testable core of resolveResumeSession.
func resolveResumeSessionInDir(arg, baseDir string) (string, error) {
	// If it looks like a raw Claude session UUID, pass through directly.
	if uuidPattern.MatchString(arg) {
		return arg, nil
	}

	// Try to resolve as a moat run name or ID by scanning stored runs.
	runIDs, err := storage.ListRunDirs(baseDir)
	if err != nil {
		return "", fmt.Errorf("listing runs: %w", err)
	}

	var match *storage.Metadata
	var matchID string

	for _, runID := range runIDs {
		store, storeErr := storage.NewRunStore(baseDir, runID)
		if storeErr != nil {
			continue
		}
		meta, metaErr := store.LoadMetadata()
		if metaErr != nil {
			continue
		}

		// Exact ID match
		if runID == arg {
			match = &meta
			matchID = runID
			break
		}

		// ID prefix match
		if strings.HasPrefix(runID, arg) && strings.HasPrefix(arg, "run_") {
			match = &meta
			matchID = runID
			// Don't break — keep looking for exact match
		}

		// Exact name match (most recent wins since ListRunDirs order is stable)
		if meta.Name == arg {
			if match == nil || meta.CreatedAt.After(match.CreatedAt) {
				match = &meta
				matchID = runID
			}
		}
	}

	if match == nil {
		return "", fmt.Errorf("no run found matching %q\n\nRun 'moat list' to see available runs.", arg)
	}

	// If the run is still active, the session ID hasn't been captured yet.
	// Tell the user to view logs or stop instead of starting a new container.
	if match.State == "running" || match.State == "starting" {
		return "", fmt.Errorf("run %s (%s) is still running\n\nUse 'moat logs %s' to view output or 'moat stop %s' to stop.", matchID, match.Name, matchID, matchID)
	}

	sessionID := match.ProviderMeta["claude_session_id"]
	if sessionID == "" {
		return "", fmt.Errorf("run %s (%s) has no recorded Claude session ID\n\nThe session ID is captured when Claude Code exits normally.", matchID, match.Name)
	}

	log.Debug("resolved --resume to claude session",
		"arg", arg,
		"runID", matchID,
		"sessionID", sessionID,
	)
	return sessionID, nil
}
