package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/andybons/agentops/internal/run"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all runs",
	Long:  `Show all runs including running, stopped, and recent runs.`,
	RunE:  listRuns,
}

func init() {
	rootCmd.AddCommand(listCmd)
}

func listRuns(cmd *cobra.Command, args []string) error {
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	runs := manager.List()

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(runs)
	}

	if len(runs) == 0 {
		fmt.Println("No runs found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tAGENT\tSTATE\tCREATED")
	for _, r := range runs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			r.ID,
			r.Agent,
			r.State,
			r.CreatedAt.Format("2006-01-02 15:04:05"),
		)
	}
	return w.Flush()
}
