package claude

import (
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
	// Validate mutually exclusive flags before delegating to shared runner
	if claudeContinue && claudeResume != "" {
		return fmt.Errorf("--continue and --resume are mutually exclusive")
	}

	return cli.RunProvider(cmd, args, cli.ProviderRunConfig{
		Name:                  "claude",
		Flags:                 &claudeFlags,
		PromptFlag:            claudePromptFlag,
		AllowedHosts:          claudeAllowedHosts,
		WtFlag:                claudeWtFlag,
		GetCredentialGrant:    getClaudeCredentialName,
		Dependencies:          []string{"node@20", "git", "claude-code"},
		NetworkHosts:          []string{"claude.ai", "*.claude.ai"},
		SupportsInitialPrompt: true,
		DryRunNote:            "Note: No API key configured. Claude will prompt for login.",
		BuildCommand: func(promptFlag, initialPrompt string) ([]string, error) {
			containerCmd := []string{"claude"}

			// By default, skip permission prompts since Moat provides isolation.
			if !claudeNoYolo {
				containerCmd = append(containerCmd, "--dangerously-skip-permissions")
			}
			if claudeContinue {
				containerCmd = append(containerCmd, "--continue")
			}
			if claudeResume != "" {
				sessionID, err := resolveResumeSession(claudeResume)
				if err != nil {
					return nil, err
				}
				containerCmd = append(containerCmd, "--resume", sessionID)
			}
			if promptFlag != "" {
				containerCmd = append(containerCmd, "-p", promptFlag)
			}
			if initialPrompt != "" {
				containerCmd = append(containerCmd, initialPrompt)
			}
			return containerCmd, nil
		},
		ConfigureAgent: func(cfg *config.Config) {
			syncLogs := true
			cfg.Claude.SyncLogs = &syncLogs
		},
	})
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
