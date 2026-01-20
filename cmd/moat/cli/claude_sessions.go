package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/andybons/moat/internal/claude"
	"github.com/andybons/moat/internal/run"
	"github.com/spf13/cobra"
)

var sessionsActiveOnly bool

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "List Claude Code sessions",
	Long: `List all Claude Code sessions.

Shows session name, workspace, state, and when it was last accessed.

Examples:
  # List all sessions
  moat claude sessions

  # List only active (running) sessions
  moat claude sessions --active`,
	RunE: runSessionsList,
}

func init() {
	claudeCmd.AddCommand(sessionsCmd)
	sessionsCmd.Flags().BoolVar(&sessionsActiveOnly, "active", false, "only show running sessions")
}

func runSessionsList(cmd *cobra.Command, args []string) error {
	sessionMgr, err := claude.NewSessionManager()
	if err != nil {
		return fmt.Errorf("creating session manager: %w", err)
	}

	sessions, err := sessionMgr.List()
	if err != nil {
		return fmt.Errorf("listing sessions: %w", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No Claude Code sessions found.")
		fmt.Println("\nStart a new session with: moat claude")
		return nil
	}

	// Get run manager to check actual run states
	runMgr, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer runMgr.Close()

	// Update session states based on actual run states
	for _, s := range sessions {
		r, err := runMgr.Get(s.RunID)
		if err != nil {
			// Run no longer exists
			if s.State == claude.SessionStateRunning {
				s.State = claude.SessionStateCompleted
				_ = sessionMgr.UpdateState(s.ID, claude.SessionStateCompleted)
			}
			continue
		}

		// Sync session state with run state
		var newState string
		switch r.State {
		case run.StateRunning:
			newState = claude.SessionStateRunning
		case run.StateStopped:
			newState = claude.SessionStateStopped
		default:
			newState = claude.SessionStateCompleted
		}

		if s.State != newState {
			s.State = newState
			_ = sessionMgr.UpdateState(s.ID, newState)
		}
	}

	// Filter if needed
	var filtered []*claude.Session
	for _, s := range sessions {
		if sessionsActiveOnly && s.State != claude.SessionStateRunning {
			continue
		}
		filtered = append(filtered, s)
	}

	if len(filtered) == 0 {
		if sessionsActiveOnly {
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
	fmt.Println("Resume a session: moat claude --resume <session>")
	fmt.Println("Attach to running: moat attach <session>")

	return nil
}

// shortenPath shortens a path for display, using ~ for home directory.
func shortenPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}

	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}

	// If still too long, truncate the middle
	const maxLen = 40
	if len(path) > maxLen {
		// Show first and last parts
		base := filepath.Base(path)
		if len(base) > maxLen-5 {
			return "..." + path[len(path)-maxLen+3:]
		}
		return "..." + path[len(path)-maxLen+3:]
	}

	return path
}

// formatTimeAgo formats a time as a human-readable "X ago" string.
func formatTimeAgo(t time.Time) string {
	d := time.Since(t)

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	case d < 24*time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
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
