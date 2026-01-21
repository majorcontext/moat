package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/andybons/moat/internal/snapshot"
	"github.com/andybons/moat/internal/storage"
	"github.com/spf13/cobra"
)

var snapshotLabel string

var snapshotCmd = &cobra.Command{
	Use:   "snapshot <run-id>",
	Short: "Create a manual snapshot of the workspace",
	Long: `Create a manual snapshot of the workspace for a running or stopped run.

Snapshots capture the current state of the workspace and can be used for
rollback or analysis. Manual snapshots are useful for creating checkpoints
before risky operations or to mark important milestones.

Examples:
  moat snapshot run_abc12345                            # Create snapshot
  moat snapshot run_abc12345 --label "before refactor"  # Create with label`,
	Args: cobra.ExactArgs(1),
	RunE: createSnapshot,
}

func init() {
	rootCmd.AddCommand(snapshotCmd)
	snapshotCmd.Flags().StringVar(&snapshotLabel, "label", "", "optional label for the snapshot")
}

func createSnapshot(cmd *cobra.Command, args []string) error {
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

	// Create engine - use workspace from metadata
	engine, err := snapshot.NewEngine(meta.Workspace, snapshotDir, snapshot.EngineOptions{})
	if err != nil {
		return fmt.Errorf("initializing snapshot engine: %w", err)
	}

	// Create the manual snapshot
	snap, err := engine.Create(snapshot.TypeManual, snapshotLabel)
	if err != nil {
		return fmt.Errorf("creating snapshot: %w", err)
	}

	// Print the snapshot ID (with label if present)
	if snap.Label != "" {
		fmt.Printf("%s (%s)\n", snap.ID, snap.Label)
	} else {
		fmt.Println(snap.ID)
	}

	return nil
}
