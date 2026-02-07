package gemini

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/log"
)

var (
	geminiFlags        cli.ExecFlags
	geminiPromptFlag   string
	geminiAllowedHosts []string
)

// NetworkHosts returns the list of hosts that Gemini needs network access to.
// These should be added to the network allow list for containers running Gemini.
func NetworkHosts() []string {
	return []string{
		"generativelanguage.googleapis.com",
		"*.googleapis.com",
		"oauth2.googleapis.com",
	}
}

// DefaultDependencies returns the default dependencies for running Gemini CLI.
func DefaultDependencies() []string {
	return []string{
		"node@20",
		"git",
		"gemini-cli",
	}
}

// RegisterCLI registers Gemini-related CLI commands.
// This adds the `moat gemini` command group with subcommands.
func (p *Provider) RegisterCLI(root *cobra.Command) {
	geminiCmd := &cobra.Command{
		Use:   "gemini [workspace] [flags]",
		Short: "Run Google Gemini CLI in an isolated container",
		Long: `Run Google Gemini CLI in an isolated container with automatic credential injection.

Your workspace is mounted at /workspace inside the container. API credentials
are injected transparently via the Moat proxy - Gemini never sees raw tokens.

Without a workspace argument, uses the current directory.

Examples:
  # Start Gemini in current directory (interactive)
  moat gemini

  # Start Gemini in a specific project
  moat gemini ./my-project

  # Ask Gemini to do something specific (non-interactive)
  moat gemini -p "explain this codebase"
  moat gemini -p "fix the bug in main.py"

  # Add additional grants (e.g., for GitHub API access)
  moat gemini --grant github

  # Name the session for easy reference
  moat gemini --name my-feature

  # Run in background
  moat gemini -d

  # Force rebuild of container image
  moat gemini --rebuild

Subcommands:
  moat gemini sessions    List Gemini sessions`,
		Args: cobra.MaximumNArgs(1),
		RunE: runGemini,
	}

	// Add shared execution flags
	cli.AddExecFlags(geminiCmd, &geminiFlags)

	// Add Gemini-specific flags
	geminiCmd.Flags().StringVarP(&geminiPromptFlag, "prompt", "p", "", "run with prompt (non-interactive mode)")
	geminiCmd.Flags().StringSliceVar(&geminiAllowedHosts, "allow-host", nil, "additional hosts to allow network access to")

	// Add sessions subcommand
	sessionsCmd := &cobra.Command{
		Use:   "sessions",
		Short: "List Gemini sessions",
		Long: `List all Gemini sessions.

Shows session name, workspace, state, and when it was last accessed.

Examples:
  # List all sessions
  moat gemini sessions

  # List only active (running) sessions
  moat gemini sessions --active`,
		RunE: runGeminiSessions,
	}
	sessionsCmd.Flags().Bool("active", false, "only show running sessions")

	geminiCmd.AddCommand(sessionsCmd)
	root.AddCommand(geminiCmd)
}

func runGemini(cmd *cobra.Command, args []string) error {
	// If subcommand is being run, don't execute this
	if cmd.CalledAs() != "gemini" {
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

	// If user has an API key stored via `moat grant gemini`, use proxy injection
	if HasCredential() {
		addGrant("gemini")
	}
	if cfg != nil {
		for _, g := range cfg.Grants {
			addGrant(g)
		}
	}
	for _, g := range geminiFlags.Grants {
		addGrant(g)
	}
	geminiFlags.Grants = grants

	// Determine interactive mode
	interactive := geminiPromptFlag == ""

	// Build container command
	var containerCmd []string
	if geminiPromptFlag != "" {
		containerCmd = []string{"gemini", "-p", geminiPromptFlag}
	} else {
		containerCmd = []string{"gemini"}
	}

	// Use name from flag, or config, or let manager generate one
	if geminiFlags.Name == "" && cfg != nil && cfg.Name != "" {
		geminiFlags.Name = cfg.Name
	}

	// Ensure dependencies for Gemini CLI
	if cfg == nil {
		cfg = &config.Config{}
	}
	for _, dep := range DefaultDependencies() {
		prefix := dep
		if idx := len(dep) - 1; idx >= 0 {
			// Extract prefix before @version
			for i := range dep {
				if dep[i] == '@' {
					prefix = dep[:i]
					break
				}
			}
		}
		if !cli.HasDependency(cfg.Dependencies, prefix) {
			cfg.Dependencies = append(cfg.Dependencies, dep)
		}
	}

	// Allow network access to Google APIs
	cfg.Network.Allow = append(cfg.Network.Allow, NetworkHosts()...)

	// Add allowed hosts if specified
	cfg.Network.Allow = append(cfg.Network.Allow, geminiAllowedHosts...)

	// Always sync Gemini logs for moat gemini command
	syncLogs := true
	cfg.Gemini.SyncLogs = &syncLogs

	// Add environment variables from flags
	if envErr := cli.ParseEnvFlags(geminiFlags.Env, cfg); envErr != nil {
		return envErr
	}

	log.Debug("starting gemini",
		"workspace", absPath,
		"grants", grants,
		"interactive", interactive,
		"prompt", geminiPromptFlag,
		"rebuild", geminiFlags.Rebuild,
	)

	if cli.DryRun {
		fmt.Println("Dry run - would start Gemini")
		fmt.Printf("Workspace: %s\n", absPath)
		fmt.Printf("Grants: %v\n", grants)
		fmt.Printf("Interactive: %v\n", interactive)
		fmt.Printf("Rebuild: %v\n", geminiFlags.Rebuild)
		if len(grants) == 0 {
			fmt.Println("Note: No API key configured. Gemini will prompt for login.")
		}
		return nil
	}

	ctx := context.Background()

	opts := cli.ExecOptions{
		Flags:       geminiFlags,
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

	if result != nil && !geminiFlags.Detach {
		fmt.Printf("Starting Gemini in %s\n", absPath)
		fmt.Printf("Session: %s (run %s)\n", result.Name, result.ID)
	}

	return nil
}

// runGeminiSessions lists Gemini sessions.
func runGeminiSessions(cmd *cobra.Command, args []string) error {
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
		fmt.Println("No Gemini sessions found.")
		fmt.Println("\nStart a new session with: moat gemini")
		return nil
	}

	// Filter if needed
	type sessionInfo struct {
		Name           string
		Workspace      string
		State          string
		LastAccessedAt time.Time
	}
	var filtered []sessionInfo
	for _, s := range sessions {
		if activeOnly && s.State != "running" {
			continue
		}
		filtered = append(filtered, sessionInfo{
			Name:           s.Name,
			Workspace:      s.Workspace,
			State:          s.State,
			LastAccessedAt: s.LastAccessedAt,
		})
	}

	if len(filtered) == 0 {
		if activeOnly {
			fmt.Println("No running Gemini sessions.")
		} else {
			fmt.Println("No Gemini sessions found.")
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
