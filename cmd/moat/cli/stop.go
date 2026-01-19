package cli

import (
	"context"
	"fmt"

	"github.com/andybons/moat/internal/run"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop [run-id]",
	Short: "Stop a running run",
	Long: `Stop a running run by its ID.

If no run-id is provided, stops the most recent running run.`,
	Args: cobra.MaximumNArgs(1),
	RunE: stopRun,
}

func init() {
	rootCmd.AddCommand(stopCmd)
}

func stopRun(cmd *cobra.Command, args []string) error {
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	var runID string
	if len(args) > 0 {
		runID = args[0]
	} else {
		// Find the most recent running run
		runs := manager.List()
		for _, r := range runs {
			if r.State == run.StateRunning {
				runID = r.ID
				break
			}
		}
		if runID == "" {
			return fmt.Errorf("no running runs found")
		}
	}

	if verbose {
		fmt.Printf("Stopping run %s...\n", runID)
	}

	if dryRun {
		fmt.Printf("Dry run - would stop run %s\n", runID)
		return nil
	}

	ctx := context.Background()
	if err := manager.Stop(ctx, runID); err != nil {
		return fmt.Errorf("stopping run: %w", err)
	}

	fmt.Printf("Run %s stopped\n", runID)
	return nil
}
