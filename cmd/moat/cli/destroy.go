package cli

import (
	"context"
	"fmt"

	"github.com/majorcontext/moat/internal/run"
	"github.com/spf13/cobra"
)

var destroyCmd = &cobra.Command{
	Use:   "destroy [run]",
	Short: "Destroy a run and its resources",
	Long: `Remove a run and clean up its container and resources.

Accepts a run ID or name. The run must be stopped before it can be destroyed.
If a name matches multiple runs, you'll be prompted to confirm.`,
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

	var runIDs []string
	if len(args) > 0 {
		var resolveErr error
		runIDs, resolveErr = resolveRunArg(manager, args[0], "Destroy")
		if resolveErr != nil {
			return resolveErr
		}
	} else {
		// Find the most recent stopped run
		runs := manager.List()
		for _, r := range runs {
			if r.GetState() == run.StateStopped {
				runIDs = []string{r.ID}
				break
			}
		}
		if len(runIDs) == 0 {
			return fmt.Errorf("no stopped runs found to destroy")
		}
	}

	ctx := context.Background()
	for _, runID := range runIDs {
		if verbose {
			fmt.Printf("Destroying run %s...\n", runID)
		}

		if dryRun {
			fmt.Printf("Dry run - would destroy run %s\n", runID)
			continue
		}

		if err := manager.Destroy(ctx, runID); err != nil {
			return fmt.Errorf("destroying run %s: %w", runID, err)
		}

		fmt.Printf("Run %s destroyed\n", runID)
	}
	return nil
}
