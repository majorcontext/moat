// cmd/agent/cli/promote.go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var promoteCmd = &cobra.Command{
	Use:   "promote [run-id]",
	Short: "Promote run artifacts to persistent storage",
	Long: `Promote preserves artifacts from a run or graduates a workspace
to a persistent state.

Examples:
  agent promote                 # Promote latest run
  agent promote run-abc123      # Promote specific run`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("promote command not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(promoteCmd)
}
