package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/majorcontext/moat/internal/claude"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/run"
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
				if updateErr := sessionMgr.UpdateState(s.ID, claude.SessionStateCompleted); updateErr != nil {
					log.Debug("failed to update session state", "session", s.ID, "error", updateErr)
				}
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
			if updateErr := sessionMgr.UpdateState(s.ID, newState); updateErr != nil {
				log.Debug("failed to update session state", "session", s.ID, "newState", newState, "error", updateErr)
			}
		}
	}

	// Filter if needed
	filtered := make([]*claude.Session, 0, len(sessions))
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
	fmt.Println("Attach to a running session: moat attach <session>")

	return nil
}
