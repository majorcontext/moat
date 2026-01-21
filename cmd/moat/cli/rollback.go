package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/andybons/moat/internal/snapshot"
	"github.com/andybons/moat/internal/storage"
	"github.com/spf13/cobra"
)

var rollbackTo string

var rollbackCmd = &cobra.Command{
	Use:   "rollback <run-id> [snapshot-id]",
	Short: "Restore a workspace from a snapshot",
	Long: `Restore a workspace from a snapshot.

If no snapshot-id is provided, the most recent snapshot is used.

By default, the restore happens in-place (modifies the current workspace).
A safety snapshot is automatically created before in-place restores so you
can undo the rollback if needed.

Use --to to extract the snapshot to a different directory instead of
restoring in-place. This is useful for comparing states or recovering
specific files without modifying the current workspace.

Examples:
  moat rollback run_abc12345                      # Restore most recent snapshot
  moat rollback run_abc12345 snap_12ab34cd        # Restore specific snapshot
  moat rollback run_abc12345 --to /tmp/recovery   # Extract to different directory`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runRollback,
}

func init() {
	rootCmd.AddCommand(rollbackCmd)
	rollbackCmd.Flags().StringVar(&rollbackTo, "to", "", "extract snapshot to a different directory instead of restoring in-place")
}

func runRollback(cmd *cobra.Command, args []string) error {
	runID := args[0]
	baseDir := storage.DefaultBaseDir()
	runDir := filepath.Join(baseDir, runID)

	// Check if run directory exists
	if _, err := os.Stat(runDir); os.IsNotExist(err) {
		return fmt.Errorf("run not found: %s", runID)
	}

	// Load run metadata to get workspace path
	store, err := storage.NewRunStore(baseDir, runID)
	if err != nil {
		return fmt.Errorf("opening run storage: %w", err)
	}

	meta, err := store.LoadMetadata()
	if err != nil {
		return fmt.Errorf("loading run metadata: %w", err)
	}

	snapshotDir := filepath.Join(runDir, "snapshots")

	// Check if snapshot directory exists
	if _, statErr := os.Stat(snapshotDir); os.IsNotExist(statErr) {
		return fmt.Errorf("no snapshots found for this run")
	}

	// Create engine - use workspace from metadata
	engine, err := snapshot.NewEngine(meta.Workspace, snapshotDir, snapshot.EngineOptions{})
	if err != nil {
		return fmt.Errorf("initializing snapshot engine: %w", err)
	}

	// Determine which snapshot to restore
	var snapshotID string
	if len(args) > 1 {
		snapshotID = args[1]
		// Verify the snapshot exists
		if _, ok := engine.Get(snapshotID); !ok {
			return fmt.Errorf("snapshot not found: %s", snapshotID)
		}
	} else {
		// Get the most recent snapshot
		var snapshots []snapshot.Metadata
		snapshots, err = engine.List()
		if err != nil {
			return fmt.Errorf("listing snapshots: %w", err)
		}
		if len(snapshots) == 0 {
			return fmt.Errorf("no snapshots found for this run")
		}
		// List() returns snapshots sorted by creation time (newest first)
		snapshotID = snapshots[0].ID
	}

	// Restore to a different directory
	if rollbackTo != "" {
		fmt.Printf("Extracting snapshot to %s... ", rollbackTo)
		if restoreErr := engine.RestoreTo(snapshotID, rollbackTo); restoreErr != nil {
			fmt.Println("error")
			return fmt.Errorf("extracting snapshot: %w", restoreErr)
		}
		fmt.Println("done")
		return nil
	}

	// In-place restore: create safety snapshot first
	fmt.Print("Creating safety snapshot of current state... ")
	safetySnap, err := engine.Create(snapshot.TypeSafety, "pre-rollback")
	if err != nil {
		fmt.Println("error")
		return fmt.Errorf("creating safety snapshot: %w", err)
	}
	fmt.Printf("done (%s)\n", safetySnap.ID)

	// Restore the snapshot
	fmt.Printf("Restoring workspace to %s... ", snapshotID)
	if err := engine.Restore(snapshotID); err != nil {
		fmt.Println("error")
		return fmt.Errorf("restoring snapshot: %w", err)
	}
	fmt.Println("done")

	fmt.Printf("\nTo undo: moat rollback %s %s\n", runID, safetySnap.ID)

	return nil
}
