package codex

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
	codexFlags        cli.ExecFlags
	codexPromptFlag   string
	codexAllowedHosts []string
	codexFullAuto     bool
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

Subcommands:
  moat codex sessions    List Codex sessions`,
		Args: cobra.MaximumNArgs(1),
		RunE: runCodex,
	}

	// Add shared execution flags
	cli.AddExecFlags(codexCmd, &codexFlags)

	// Add Codex-specific flags
	codexCmd.Flags().StringVarP(&codexPromptFlag, "prompt", "p", "", "run with prompt (non-interactive mode)")
	codexCmd.Flags().StringSliceVar(&codexAllowedHosts, "allow-host", nil, "additional hosts to allow network access to")
	codexCmd.Flags().BoolVar(&codexFullAuto, "full-auto", true, "enable full-auto mode (auto-approve tool use); set to false for manual approval")

	// Add sessions subcommand
	sessionsCmd := &cobra.Command{
		Use:   "sessions",
		Short: "List Codex sessions",
		Long: `List all Codex sessions.

Shows session name, workspace, state, and when it was last accessed.

Examples:
  # List all sessions
  moat codex sessions

  # List only active (running) sessions
  moat codex sessions --active`,
		RunE: runCodexSessions,
	}
	sessionsCmd.Flags().Bool("active", false, "only show running sessions")

	codexCmd.AddCommand(sessionsCmd)
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

	if result != nil && !codexFlags.Detach {
		fmt.Printf("Starting Codex CLI in %s\n", absPath)
		fmt.Printf("Session: %s (run %s)\n", result.Name, result.ID)
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

// runCodexSessions lists Codex sessions.
func runCodexSessions(cmd *cobra.Command, args []string) error {
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
		fmt.Println("No Codex sessions found.")
		fmt.Println("\nStart a new session with: moat codex")
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
			fmt.Println("No running Codex sessions.")
		} else {
			fmt.Println("No Codex sessions found.")
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
