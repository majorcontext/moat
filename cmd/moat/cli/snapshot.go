package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/snapshot"
	"github.com/majorcontext/moat/internal/storage"
	"github.com/spf13/cobra"
)

var (
	snapshotLabel     string
	snapshotPruneKeep int
	snapshotRestoreTo string
)

var snapshotCmd = &cobra.Command{
	Use:   "snapshot <run>",
	Short: "Create and manage workspace snapshots",
	Long: `Create and manage workspace snapshots.

Accepts a run ID or name. When called with an argument, creates a manual
snapshot of the workspace. Use subcommands to list, prune, or restore snapshots.

Examples:
  moat snapshot my-agent                                     # Create snapshot by name
  moat snapshot run_a1b2c3d4e5f6                             # Create snapshot by ID
  moat snapshot run_a1b2c3d4e5f6 --label "before refactor"   # Create with label
  moat snapshot list run_a1b2c3d4e5f6                         # List snapshots
  moat snapshot prune run_a1b2c3d4e5f6                        # Prune old snapshots
  moat snapshot restore run_a1b2c3d4e5f6                      # Restore most recent`,
	Args: cobra.ExactArgs(1),
	RunE: createSnapshot,
}

var snapshotListCmd = &cobra.Command{
	Use:   "list <run-id>",
	Short: "List snapshots for a run",
	Long: `List all snapshots captured for a specific run.

Snapshots are point-in-time captures of the workspace that can be used
for restore or analysis. They are created automatically (pre-run) and
can also be created manually or triggered by events (git commits, builds).

Examples:
  moat snapshot list run_a1b2c3d4e5f6          # List all snapshots for this run
  moat snapshot list run_a1b2c3d4e5f6 --json   # Output as JSON`,
	Args: cobra.ExactArgs(1),
	RunE: listSnapshots,
}

var snapshotPruneCmd = &cobra.Command{
	Use:   "prune <run-id>",
	Short: "Remove old snapshots, keeping the newest N",
	Long: `Remove old snapshots while keeping the most recent ones.

The pre-run snapshot is always preserved regardless of the --keep value.
This ensures you can always restore to the original workspace state.

Examples:
  moat snapshot prune run_a1b2c3d4e5f6            # Keep 5 most recent (default)
  moat snapshot prune run_a1b2c3d4e5f6 --keep=3   # Keep 3 most recent
  moat snapshot prune run_a1b2c3d4e5f6 --dry-run  # Show what would be deleted`,
	Args: cobra.ExactArgs(1),
	RunE: pruneSnapshots,
}

var snapshotRestoreCmd = &cobra.Command{
	Use:   "restore <run-id> [snapshot-id]",
	Short: "Restore a workspace from a snapshot",
	Long: `Restore a workspace from a snapshot.

If no snapshot-id is provided, the most recent snapshot is used.

By default, the restore happens in-place (modifies the current workspace).
A safety snapshot is automatically created before in-place restores so you
can undo the restore if needed.

Use --to to extract the snapshot to a different directory instead of
restoring in-place. This is useful for comparing states or recovering
specific files without modifying the current workspace.

Examples:
  moat snapshot restore run_a1b2c3d4e5f6                      # Restore most recent snapshot
  moat snapshot restore run_a1b2c3d4e5f6 snap_a1b2c3d4e5f6    # Restore specific snapshot
  moat snapshot restore run_a1b2c3d4e5f6 --to /tmp/recovery   # Extract to different directory`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runSnapshotRestore,
}

func init() {
	rootCmd.AddCommand(snapshotCmd)
	snapshotCmd.Flags().StringVar(&snapshotLabel, "label", "", "optional label for the snapshot")

	snapshotCmd.AddCommand(snapshotListCmd)

	snapshotCmd.AddCommand(snapshotPruneCmd)
	snapshotPruneCmd.Flags().IntVar(&snapshotPruneKeep, "keep", 5, "number of snapshots to keep (excluding pre-run)")

	snapshotCmd.AddCommand(snapshotRestoreCmd)
	snapshotRestoreCmd.Flags().StringVar(&snapshotRestoreTo, "to", "", "extract snapshot to a different directory instead of restoring in-place")
}

// resolveSnapshotRunID resolves a run argument for snapshot commands.
func resolveSnapshotRunID(arg string) (string, error) {
	manager, err := run.NewManager()
	if err != nil {
		return "", fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()
	return resolveRunArgSingle(manager, arg)
}

func createSnapshot(cmd *cobra.Command, args []string) error {
	runID, err := resolveSnapshotRunID(args[0])
	if err != nil {
		return err
	}
	baseDir := storage.DefaultBaseDir()
	runDir := filepath.Join(baseDir, runID)

	// Check if run directory exists
	if _, statErr := os.Stat(runDir); os.IsNotExist(statErr) {
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

func listSnapshots(cmd *cobra.Command, args []string) error {
	runID, err := resolveSnapshotRunID(args[0])
	if err != nil {
		return err
	}
	baseDir := storage.DefaultBaseDir()
	runDir := filepath.Join(baseDir, runID)

	// Check if run directory exists
	if _, statErr := os.Stat(runDir); os.IsNotExist(statErr) {
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
		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode([]snapshot.Metadata{})
		}
		fmt.Println("No snapshots found for this run")
		return nil
	}

	// Create engine - use workspace from metadata
	engine, err := snapshot.NewEngine(meta.Workspace, snapshotDir, snapshot.EngineOptions{})
	if err != nil {
		return fmt.Errorf("initializing snapshot engine: %w", err)
	}

	snapshots, err := engine.List()
	if err != nil {
		return fmt.Errorf("listing snapshots: %w", err)
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(snapshots)
	}

	if len(snapshots) == 0 {
		fmt.Println("No snapshots found for this run")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTYPE\tLABEL\tCREATED")
	for _, s := range snapshots {
		label := s.Label
		if label == "" {
			label = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			s.ID,
			s.Type,
			label,
			formatAge(s.CreatedAt),
		)
	}
	w.Flush()

	fmt.Printf("\n%d snapshots (backend: %s)\n", len(snapshots), engine.Backend().Name())

	return nil
}

func pruneSnapshots(cmd *cobra.Command, args []string) error {
	runID, err := resolveSnapshotRunID(args[0])
	if err != nil {
		return err
	}
	baseDir := storage.DefaultBaseDir()
	runDir := filepath.Join(baseDir, runID)

	// Check if run directory exists
	if _, statErr := os.Stat(runDir); os.IsNotExist(statErr) {
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
		fmt.Println("No snapshots found for this run")
		return nil
	}

	// Create engine
	engine, err := snapshot.NewEngine(meta.Workspace, snapshotDir, snapshot.EngineOptions{})
	if err != nil {
		return fmt.Errorf("initializing snapshot engine: %w", err)
	}

	snapshots, err := engine.List()
	if err != nil {
		return fmt.Errorf("listing snapshots: %w", err)
	}

	if len(snapshots) == 0 {
		fmt.Println("No snapshots to prune")
		return nil
	}

	// Separate pre-run from other snapshots
	var preRunSnapshots []snapshot.Metadata
	var otherSnapshots []snapshot.Metadata
	for _, s := range snapshots {
		if s.Type == snapshot.TypePreRun {
			preRunSnapshots = append(preRunSnapshots, s)
		} else {
			otherSnapshots = append(otherSnapshots, s)
		}
	}

	// Sort other snapshots by creation time (newest first) - List() already does this,
	// but let's be explicit for the pruning logic
	sort.Slice(otherSnapshots, func(i, j int) bool {
		return otherSnapshots[i].CreatedAt.After(otherSnapshots[j].CreatedAt)
	})

	// Determine which snapshots to delete (keep newest N)
	var toDelete []snapshot.Metadata
	if len(otherSnapshots) > snapshotPruneKeep {
		toDelete = otherSnapshots[snapshotPruneKeep:]
	}

	if len(toDelete) == 0 {
		fmt.Printf("Nothing to prune (have %d snapshots, keeping %d + %d pre-run)\n",
			len(otherSnapshots), snapshotPruneKeep, len(preRunSnapshots))
		return nil
	}

	// Show what will be deleted
	fmt.Printf("Snapshots to delete (%d):\n", len(toDelete))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, s := range toDelete {
		label := s.Label
		if label == "" {
			label = "-"
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n",
			s.ID,
			s.Type,
			label,
			formatAge(s.CreatedAt),
		)
	}
	w.Flush()
	fmt.Println()

	// Dry run - just show what would be deleted
	if dryRun {
		fmt.Println("Dry run - no changes made")
		return nil
	}

	// Delete snapshots
	var deleted, failed int
	for _, s := range toDelete {
		fmt.Printf("Deleting %s... ", s.ID)
		if err := engine.Delete(s.ID); err != nil {
			fmt.Printf("error: %v\n", err)
			failed++
			continue
		}
		fmt.Println("done")
		deleted++
	}

	if failed > 0 {
		fmt.Printf("\nPruned %d snapshots (%d failed)\n", deleted, failed)
		return fmt.Errorf("failed to delete %d of %d snapshots", failed, len(toDelete))
	}

	fmt.Printf("\nPruned %d snapshots\n", deleted)
	return nil
}

func runSnapshotRestore(cmd *cobra.Command, args []string) error {
	runID, err := resolveSnapshotRunID(args[0])
	if err != nil {
		return err
	}
	baseDir := storage.DefaultBaseDir()
	runDir := filepath.Join(baseDir, runID)

	// Check if run directory exists
	if _, statErr := os.Stat(runDir); os.IsNotExist(statErr) {
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
	if snapshotRestoreTo != "" {
		fmt.Printf("Extracting snapshot to %s... ", snapshotRestoreTo)
		if restoreErr := engine.RestoreTo(snapshotID, snapshotRestoreTo); restoreErr != nil {
			fmt.Println("error")
			return fmt.Errorf("extracting snapshot: %w", restoreErr)
		}
		fmt.Println("done")
		return nil
	}

	// In-place restore: create safety snapshot first
	fmt.Print("Creating safety snapshot of current state... ")
	safetySnap, err := engine.Create(snapshot.TypeSafety, "pre-restore")
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

	fmt.Printf("\nTo undo: moat snapshot restore %s %s\n", runID, safetySnap.ID)

	return nil
}
