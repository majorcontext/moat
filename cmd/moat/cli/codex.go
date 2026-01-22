package cli

import (
	"fmt"

	"github.com/andybons/moat/internal/codex"
	"github.com/spf13/cobra"
)

var codexCmd = &cobra.Command{
	Use:   "codex",
	Short: "Run OpenAI Codex CLI in an isolated container",
	Long: `Run OpenAI Codex CLI in an isolated container with automatic credential injection.

Your workspace is mounted at /workspace inside the container. API credentials
are injected transparently via the Moat proxy - Codex never sees raw tokens.

By default, Codex runs with --full-auto since the container provides isolation.
Use --noyolo to require manual approval for each tool use.

Without a workspace argument, uses the current directory.

Examples:
  # Start Codex in current directory (interactive)
  moat codex

  # Start Codex in a specific project
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

  # Require manual approval for each tool use (disable full-auto mode)
  moat codex --noyolo

Subcommands:
  moat codex sessions        List Codex sessions`,
}

var codexSessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "List Codex sessions",
	Long: `List all Codex sessions.

Shows the session history with workspace, status, and last accessed time.

Examples:
  # List all sessions
  moat codex sessions

  # Show only active sessions
  moat codex sessions --active`,
	RunE: runCodexSessions,
}

var codexSessionsActive bool

func init() {
	rootCmd.AddCommand(codexCmd)

	codexCmd.AddCommand(codexSessionsCmd)
	codexSessionsCmd.Flags().BoolVar(&codexSessionsActive, "active", false, "show only running sessions")
}

func runCodexSessions(cmd *cobra.Command, args []string) error {
	sessionMgr, err := codex.NewSessionManager()
	if err != nil {
		return fmt.Errorf("creating session manager: %w", err)
	}

	sessions, err := sessionMgr.List()
	if err != nil {
		return fmt.Errorf("listing sessions: %w", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No Codex sessions found.")
		fmt.Println("\nStart a new session with: moat codex [workspace]")
		return nil
	}

	// Filter if --active flag is set
	if codexSessionsActive {
		var active []*codex.Session
		for _, s := range sessions {
			if s.State == codex.SessionStateRunning {
				active = append(active, s)
			}
		}
		sessions = active

		if len(sessions) == 0 {
			fmt.Println("No active Codex sessions.")
			return nil
		}
	}

	fmt.Printf("Codex sessions:\n\n")

	// Find max name length for alignment
	maxNameLen := 4 // minimum "NAME"
	for _, s := range sessions {
		if len(s.Name) > maxNameLen {
			maxNameLen = len(s.Name)
		}
	}

	// Header
	fmt.Printf("  %-*s  %-10s  %-20s  %s\n", maxNameLen, "NAME", "STATE", "LAST ACCESS", "WORKSPACE")
	fmt.Printf("  %-*s  %-10s  %-20s  %s\n", maxNameLen, "----", "-----", "-----------", "---------")

	for _, s := range sessions {
		lastAccess := formatTimeAgo(s.LastAccessedAt)
		fmt.Printf("  %-*s  %-10s  %-20s  %s\n", maxNameLen, s.Name, s.State, lastAccess, s.Workspace)
	}

	return nil
}
