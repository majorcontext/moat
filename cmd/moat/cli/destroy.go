package cli

import (
	"context"
	"fmt"

	"github.com/andybons/moat/internal/run"
	"github.com/spf13/cobra"
)

var destroyCmd = &cobra.Command{
	Use:   "destroy [run-id]",
	Short: "Destroy a run and its resources",
	Long: `Remove a run and clean up its container and resources.

The run must be stopped before it can be destroyed.`,
	Args: cobra.MaximumNArgs(1),
	RunE: destroyRun,
}

func init() {
	rootCmd.AddCommand(destroyCmd)
}

func destroyRun(cmd *cobra.Command, args []string) error {
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	var runID string
	if len(args) > 0 {
		runID = args[0]
	} else {
		// Find the most recent stopped run
		runs := manager.List()
		for _, r := range runs {
			if r.State == run.StateStopped {
				runID = r.ID
				break
			}
		}
		if runID == "" {
			return fmt.Errorf("no stopped runs found to destroy")
		}
	}

	if verbose {
		fmt.Printf("Destroying run %s...\n", runID)
	}

	if dryRun {
		fmt.Printf("Dry run - would destroy run %s\n", runID)
		return nil
	}

	ctx := context.Background()
	if err := manager.Destroy(ctx, runID); err != nil {
		return fmt.Errorf("destroying run: %w", err)
	}

	fmt.Printf("Run %s destroyed\n", runID)
	return nil
}
