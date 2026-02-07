package claude

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
)

var (
	claudeFlags        cli.ExecFlags
	claudePromptFlag   string
	claudeAllowedHosts []string
	claudeNoYolo       bool
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

Subcommands:
  moat claude sessions    List Claude Code sessions`,
		Args: cobra.MaximumNArgs(1),
		RunE: runClaudeCode,
	}

	// Add shared execution flags
	cli.AddExecFlags(claudeCmd, &claudeFlags)

	// Add Claude-specific flags
	claudeCmd.Flags().StringVarP(&claudePromptFlag, "prompt", "p", "", "run with prompt (non-interactive mode)")
	claudeCmd.Flags().StringSliceVar(&claudeAllowedHosts, "allow-host", nil, "additional hosts to allow network access to")
	claudeCmd.Flags().BoolVar(&claudeNoYolo, "noyolo", false, "disable --dangerously-skip-permissions (require manual approval for each tool use)")

	// Add sessions subcommand
	sessionsCmd := &cobra.Command{
		Use:   "sessions",
		Short: "List Claude Code sessions",
		Long: `List all Claude Code sessions.

Shows session name, workspace, state, and when it was last accessed.

Examples:
  # List all sessions
  moat claude sessions

  # List only active (running) sessions
  moat claude sessions --active`,
		RunE: runSessions,
	}
	sessionsCmd.Flags().Bool("active", false, "only show running sessions")

	claudeCmd.AddCommand(sessionsCmd)
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
		OnRunCreated: func(info cli.RunInfo) {
			// Create session record
			sessionMgr, sessionErr := NewSessionManager()
			if sessionErr != nil {
				log.Debug("failed to create session manager", "error", sessionErr)
				return
			}
			if _, sessionErr = sessionMgr.Create(absPath, info.ID, info.Name, grants); sessionErr != nil {
				log.Debug("failed to create session", "error", sessionErr)
			}
		},
	}

	result, err := cli.ExecuteRun(ctx, opts)
	if err != nil {
		return err
	}

	if result != nil && !claudeFlags.Detach {
		fmt.Printf("Starting Claude Code in %s\n", absPath)
		fmt.Printf("Session: %s (run %s)\n", result.Name, result.ID)
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

// runSessions lists Claude Code sessions.
func runSessions(cmd *cobra.Command, args []string) error {
	activeOnly, _ := cmd.Flags().GetBool("active")

	mgr, err := NewSessionManager()
	if err != nil {
		return fmt.Errorf("creating session manager: %w", err)
	}

	sessions, err := mgr.List()
	if err != nil {
		return fmt.Errorf("listing sessions: %w", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No Claude Code sessions found.")
		fmt.Println("\nStart a new session with: moat claude")
		return nil
	}

	// Filter if needed
	var filtered []*struct {
		ID             string
		Name           string
		Workspace      string
		State          string
		LastAccessedAt time.Time
	}
	for _, s := range sessions {
		if activeOnly && s.State != "running" {
			continue
		}
		filtered = append(filtered, &struct {
			ID             string
			Name           string
			Workspace      string
			State          string
			LastAccessedAt time.Time
		}{
			ID:             s.ID,
			Name:           s.Name,
			Workspace:      s.Workspace,
			State:          s.State,
			LastAccessedAt: s.LastAccessedAt,
		})
	}

	if len(filtered) == 0 {
		if activeOnly {
			fmt.Println("No running Claude Code sessions.")
		} else {
			fmt.Println("No Claude Code sessions found.")
		}
		return nil
	}

	// Print as table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION\tWORKSPACE\tSTATE\tLAST ACCESSED")

	for _, s := range filtered {
		workspace := shortenPath(s.Workspace)
		lastAccessed := formatTimeAgo(s.LastAccessedAt)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.Name, workspace, s.State, lastAccessed)
	}

	w.Flush()

	fmt.Println()
	fmt.Println("Attach to a running session: moat attach <session>")

	return nil
}

// shortenPath shortens a path for display.
func shortenPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}

	if len(path) > len(home) && path[:len(home)] == home {
		return "~" + path[len(home):]
	}
	return path
}

// formatTimeAgo formats a time as a human-readable "ago" string.
func formatTimeAgo(t time.Time) string {
	if t.IsZero() {
		return "never"
	}

	d := time.Since(t)

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	case d < 7*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		return t.Format("Jan 2, 2006")
	}
}
