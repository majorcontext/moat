package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/majorcontext/moat/internal/claude"
	"github.com/majorcontext/moat/internal/codex"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/run"
	"github.com/spf13/cobra"
)

var (
	sessionsAgent      string
	sessionsActiveFlag bool
)

var allSessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "List all agent sessions",
	Long: `List sessions across all agent types.

Shows session name, agent type, workspace, state, and when it was last accessed.

Examples:
  moat sessions                  # List all sessions
  moat sessions --active         # List only running sessions
  moat sessions --agent claude   # List only Claude sessions
  moat sessions --agent codex    # List only Codex sessions`,
	RunE: runAllSessions,
}

func init() {
	rootCmd.AddCommand(allSessionsCmd)
	allSessionsCmd.Flags().StringVar(&sessionsAgent, "agent", "", "filter by agent type (claude, codex)")
	allSessionsCmd.Flags().BoolVar(&sessionsActiveFlag, "active", false, "only show running sessions")
}

type sessionEntry struct {
	Name         string
	Agent        string
	Workspace    string
	State        string
	LastAccessed string
}

func runAllSessions(cmd *cobra.Command, args []string) error {
	var entries []sessionEntry

	if sessionsAgent == "" || sessionsAgent == "claude" {
		claudeEntries, err := getClaudeSessions()
		if err != nil {
			log.Debug("failed to list claude sessions", "error", err)
		} else {
			entries = append(entries, claudeEntries...)
		}
	}

	if sessionsAgent == "" || sessionsAgent == "codex" {
		codexEntries, err := getCodexSessions()
		if err != nil {
			log.Debug("failed to list codex sessions", "error", err)
		} else {
			entries = append(entries, codexEntries...)
		}
	}

	if sessionsAgent != "" && sessionsAgent != "claude" && sessionsAgent != "codex" {
		return fmt.Errorf("unknown agent type: %s (supported: claude, codex)", sessionsAgent)
	}

	// Filter active
	if sessionsActiveFlag {
		var active []sessionEntry
		for _, e := range entries {
			if e.State == claude.SessionStateRunning || e.State == codex.SessionStateRunning {
				active = append(active, e)
			}
		}
		entries = active
	}

	if len(entries) == 0 {
		if sessionsActiveFlag {
			fmt.Println("No running sessions.")
		} else {
			fmt.Println("No sessions found.")
		}
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION\tAGENT\tWORKSPACE\tSTATE\tLAST ACCESSED")
	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			e.Name, e.Agent, e.Workspace, e.State, e.LastAccessed)
	}
	w.Flush()

	return nil
}

func getClaudeSessions() ([]sessionEntry, error) {
	sessionMgr, err := claude.NewSessionManager()
	if err != nil {
		return nil, err
	}

	sessions, err := sessionMgr.List()
	if err != nil {
		return nil, err
	}

	// Sync states with run manager
	runMgr, err := run.NewManager()
	if err == nil {
		defer runMgr.Close()
		for _, s := range sessions {
			r, err := runMgr.Get(s.RunID)
			if err != nil {
				if s.State == claude.SessionStateRunning {
					s.State = claude.SessionStateCompleted
					_ = sessionMgr.UpdateState(s.ID, claude.SessionStateCompleted)
				}
				continue
			}
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
	}

	entries := make([]sessionEntry, 0, len(sessions))
	for _, s := range sessions {
		entries = append(entries, sessionEntry{
			Name:         s.Name,
			Agent:        "claude",
			Workspace:    shortenPath(s.Workspace),
			State:        s.State,
			LastAccessed: formatTimeAgo(s.LastAccessedAt),
		})
	}
	return entries, nil
}

func getCodexSessions() ([]sessionEntry, error) {
	sessionMgr, err := codex.NewSessionManager()
	if err != nil {
		return nil, err
	}

	sessions, err := sessionMgr.List()
	if err != nil {
		return nil, err
	}

	entries := make([]sessionEntry, 0, len(sessions))
	for _, s := range sessions {
		entries = append(entries, sessionEntry{
			Name:         s.Name,
			Agent:        "codex",
			Workspace:    shortenPath(s.Workspace),
			State:        s.State,
			LastAccessed: formatTimeAgo(s.LastAccessedAt),
		})
	}
	return entries, nil
}
