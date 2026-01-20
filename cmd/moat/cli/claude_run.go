package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/andybons/moat/internal/claude"
	"github.com/andybons/moat/internal/config"
	"github.com/andybons/moat/internal/credential"
	"github.com/andybons/moat/internal/log"
	"github.com/andybons/moat/internal/run"
	"github.com/andybons/moat/internal/term"
	"github.com/spf13/cobra"
)

var (
	claudeFlags        ExecFlags
	claudePromptFlag   string
	claudeResumeFlag   string
	claudeAllowedHosts []string
)

func init() {
	// Add the run functionality directly to claudeCmd
	claudeCmd.RunE = runClaudeCode
	claudeCmd.Args = cobra.MaximumNArgs(1)

	// Update usage and examples
	claudeCmd.Use = "claude [workspace] [flags]"
	claudeCmd.Long = `Run Claude Code in an isolated container with automatic credential injection.

Your workspace is mounted at /workspace inside the container. API credentials
are injected transparently via the Moat proxy - Claude Code never sees raw tokens.

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

  # Resume a previous session
  moat claude --resume
  moat claude --resume my-feature

Subcommands:
  moat claude plugins list         List configured plugins
  moat claude marketplace list     List marketplaces
  moat claude marketplace update   Update marketplace caches`

	// Add shared execution flags
	AddExecFlags(claudeCmd, &claudeFlags)

	// Add Claude-specific flags
	claudeCmd.Flags().StringVarP(&claudePromptFlag, "prompt", "p", "", "run with prompt (non-interactive mode)")
	claudeCmd.Flags().StringVarP(&claudeResumeFlag, "resume", "r", "", "resume a previous session (by name or ID, or empty for most recent)")
	claudeCmd.Flags().StringSliceVar(&claudeAllowedHosts, "allow-host", nil, "additional hosts to allow network access to")
}

func runClaudeCode(cmd *cobra.Command, args []string) error {
	// If subcommand is being run, don't execute this
	if cmd.CalledAs() != "claude" || len(args) > 0 && (args[0] == "plugins" || args[0] == "marketplace") {
		return nil
	}

	// Determine workspace
	workspace := "."
	if len(args) > 0 {
		workspace = args[0]
	}

	absPath, err := filepath.Abs(workspace)
	if err != nil {
		return fmt.Errorf("resolving workspace path: %w", err)
	}

	// Verify path exists and is a directory
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("workspace path %q: %w", absPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace path %q is not a directory", absPath)
	}

	// Handle resume flag
	if cmd.Flags().Changed("resume") || claudeResumeFlag != "" {
		return handleClaudeResume(absPath, claudeResumeFlag)
	}

	// Determine authentication mode:
	// 1. API key via Moat grant (requires --grant anthropic, uses proxy injection)
	// 2. OAuth via ~/.claude/.credentials.json (Pro/Max subscription, no proxy needed)
	hasAPIKey := hasAnthropicCredential()
	hasOAuth := hasClaudeOAuthCredentials()

	if !hasAPIKey && !hasOAuth {
		return fmt.Errorf(`No Claude authentication found.

For Claude Pro/Max subscription:
  Log in with: claude /login
  (Credentials are stored in ~/.claude/.credentials.json)

For API key (pay-as-you-go):
  moat grant anthropic
  (Get a key from https://console.anthropic.com/)`)
	}

	// Load agent.yaml if present, otherwise use defaults
	cfg, err := config.Load(absPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Build grants list based on auth mode
	var grants []string
	if hasAPIKey {
		// API key mode: use proxy injection
		grants = append(grants, "anthropic")
	}
	// If only OAuth, no anthropic grant needed - Claude handles its own auth
	if cfg != nil {
		for _, g := range cfg.Grants {
			if g != "anthropic" {
				grants = append(grants, g)
			}
		}
	}
	// Add grants from --grant flag
	for _, g := range claudeFlags.Grants {
		// Avoid duplicates
		found := false
		for _, existing := range grants {
			if existing == g {
				found = true
				break
			}
		}
		if !found {
			grants = append(grants, g)
		}
	}
	// Update flags with merged grants
	claudeFlags.Grants = grants

	// Build container command
	containerCmd := []string{"npx", "@anthropic-ai/claude-code"}
	if claudePromptFlag != "" {
		containerCmd = append(containerCmd, "-p", claudePromptFlag)
	}

	// Determine interactive mode
	interactive := claudePromptFlag == ""

	// Use name from flag, or config, or let manager generate one
	if claudeFlags.Name == "" && cfg != nil && cfg.Name != "" {
		claudeFlags.Name = cfg.Name
	}

	// Ensure node and git dependencies for Claude Code
	if cfg == nil {
		cfg = &config.Config{}
	}
	if !hasDependency(cfg.Dependencies, "node") {
		cfg.Dependencies = append(cfg.Dependencies, "node@20")
	}
	if !hasDependency(cfg.Dependencies, "git") {
		cfg.Dependencies = append(cfg.Dependencies, "git")
	}

	// Always sync Claude logs for moat claude command
	syncLogs := true
	cfg.Claude.SyncLogs = &syncLogs

	// For OAuth mode, mount credentials and allow claude.ai access
	if hasOAuth && !hasAPIKey {
		// Signal to run manager to mount credentials with correct container home path
		cfg.Claude.UseOAuth = true

		// OAuth mode needs network access to claude.ai
		cfg.Network.Allow = append(cfg.Network.Allow, "claude.ai", "*.claude.ai")
	}

	// Add allowed hosts if specified
	for _, host := range claudeAllowedHosts {
		cfg.Network.Allow = append(cfg.Network.Allow, host)
	}

	// Add environment variables from flags
	if len(claudeFlags.Env) > 0 {
		// Merge into config (env from flags takes precedence)
		if cfg.Env == nil {
			cfg.Env = make(map[string]string)
		}
		for _, e := range claudeFlags.Env {
			// Parse KEY=VALUE
			for i := 0; i < len(e); i++ {
				if e[i] == '=' {
					cfg.Env[e[:i]] = e[i+1:]
					break
				}
			}
		}
	}

	// Determine auth mode for logging
	authMode := "api-key"
	if hasOAuth && !hasAPIKey {
		authMode = "oauth"
	} else if hasOAuth && hasAPIKey {
		authMode = "api-key (oauth available)"
	}

	log.Debug("starting claude code",
		"workspace", absPath,
		"grants", grants,
		"interactive", interactive,
		"prompt", claudePromptFlag,
		"auth", authMode,
		"rebuild", claudeFlags.Rebuild,
	)

	if dryRun {
		fmt.Println("Dry run - would start Claude Code")
		fmt.Printf("Workspace: %s\n", absPath)
		fmt.Printf("Auth: %s\n", authMode)
		fmt.Printf("Grants: %v\n", grants)
		fmt.Printf("Interactive: %v\n", interactive)
		fmt.Printf("Rebuild: %v\n", claudeFlags.Rebuild)
		if hasOAuth && !hasAPIKey {
			fmt.Println("Note: Using OAuth credentials from ~/.claude/.credentials.json")
		}
		return nil
	}

	ctx := context.Background()

	opts := ExecOptions{
		Flags:       claudeFlags,
		Workspace:   absPath,
		Command:     containerCmd,
		Config:      cfg,
		Interactive: interactive,
		TTY:         interactive,
		OnRunCreated: func(r *run.Run) {
			// Create session record
			sessionMgr, err := claude.NewSessionManager()
			if err != nil {
				log.Debug("failed to create session manager", "error", err)
				return
			}
			if _, err := sessionMgr.Create(absPath, r.ID, r.Name, grants); err != nil {
				log.Debug("failed to create session", "error", err)
			}
		},
	}

	r, err := ExecuteRun(ctx, opts)
	if err != nil {
		return err
	}

	if r != nil && !claudeFlags.Detach {
		fmt.Printf("Starting Claude Code in %s\n", absPath)
		fmt.Printf("Session: %s (run %s)\n", r.Name, r.ID)
	}

	return nil
}

// hasAnthropicCredential checks if an anthropic credential is stored via Moat grant.
func hasAnthropicCredential() bool {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return false
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return false
	}
	_, err = store.Get(credential.ProviderAnthropic)
	return err == nil
}

// hasClaudeOAuthCredentials checks if Claude Code OAuth credentials exist.
// These are stored by `claude /login` for Pro/Max subscriptions.
func hasClaudeOAuthCredentials() bool {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}

	credentialsPath := filepath.Join(homeDir, ".claude", ".credentials.json")
	info, err := os.Stat(credentialsPath)
	if err != nil {
		return false
	}

	// Check file is not empty
	return info.Size() > 0
}

// hasDependency checks if a dependency prefix exists in the list.
func hasDependency(deps []string, prefix string) bool {
	for _, d := range deps {
		if d == prefix || len(d) > len(prefix) && d[:len(prefix)+1] == prefix+"@" {
			return true
		}
	}
	return false
}

// handleClaudeResume handles resuming a previous session.
func handleClaudeResume(workspace, idOrName string) error {
	sessionMgr, err := claude.NewSessionManager()
	if err != nil {
		return fmt.Errorf("creating session manager: %w", err)
	}

	// Find session
	var session *claude.Session
	if idOrName != "" {
		session, err = sessionMgr.Get(idOrName)
	} else {
		session, err = sessionMgr.GetByWorkspace(workspace)
	}

	if err != nil {
		return fmt.Errorf("finding session: %w\n\nStart a new session with: moat claude", err)
	}

	// Check if run is still active
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	r, err := manager.Get(session.RunID)
	if err == nil && r.State == run.StateRunning {
		// Attach to existing run
		fmt.Printf("Attaching to running session %s...\n", session.Name)

		// Update session access time
		_ = sessionMgr.Touch(session.ID)

		// Put terminal in raw mode
		ctx := context.Background()
		if term.IsTerminal(os.Stdin) {
			rawState, err := term.EnableRawMode(os.Stdin)
			if err != nil {
				log.Debug("failed to enable raw mode", "error", err)
			} else {
				defer func() {
					if err := term.RestoreTerminal(rawState); err != nil {
						log.Debug("failed to restore terminal", "error", err)
					}
				}()
			}
		}

		fmt.Printf("%s\n\n", term.EscapeHelpText())

		escapeProxy := term.NewEscapeProxy(os.Stdin)
		return manager.Attach(ctx, r.ID, escapeProxy, os.Stdout, os.Stderr)
	}

	// Session's run is not running - offer to start new
	fmt.Printf("Session %s is not running (last state: %s)\n", session.Name, session.State)
	fmt.Printf("Workspace: %s\n", session.Workspace)
	fmt.Printf("\nTo start a new session: moat claude %s\n", session.Workspace)

	return nil
}
