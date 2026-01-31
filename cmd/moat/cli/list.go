package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/majorcontext/moat/internal/run"
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
	fmt.Fprintln(w, "NAME\tRUN ID\tSTATE\tENDPOINTS")
	for _, r := range runs {
		endpoints := ""
		if len(r.Ports) > 0 {
			names := make([]string, 0, len(r.Ports))
			for name := range r.Ports {
				names = append(names, name)
			}
			endpoints = strings.Join(names, ", ")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			r.Name,
			r.ID,
			r.State,
			endpoints,
		)
	}
	return w.Flush()
}
