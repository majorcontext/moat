package cli

import (
	"context"
	"fmt"

	"github.com/majorcontext/moat/internal/run"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop [run]",
	Short: "Stop a running run",
	Long: `Stop a running run by its ID or name.

If no argument is provided, stops the most recent running run.
If a name matches multiple runs, you'll be prompted to confirm.`,
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

	var runIDs []string
	if len(args) > 0 {
		var resolveErr error
		runIDs, resolveErr = resolveRunArg(manager, args[0], "Stop")
		if resolveErr != nil {
			return resolveErr
		}
	} else {
		// Find the most recent running run
		runs := manager.List()
		for _, r := range runs {
			if r.State == run.StateRunning {
				runIDs = []string{r.ID}
				break
			}
		}
		if len(runIDs) == 0 {
			return fmt.Errorf("no running runs found")
		}
	}

	ctx := context.Background()
	for _, runID := range runIDs {
		if verbose {
			fmt.Printf("Stopping run %s...\n", runID)
		}

		if dryRun {
			fmt.Printf("Dry run - would stop run %s\n", runID)
			continue
		}

		if err := manager.Stop(ctx, runID); err != nil {
			return fmt.Errorf("stopping run %s: %w", runID, err)
		}

		fmt.Printf("Run %s stopped\n", runID)
	}
	return nil
}
