package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/andybons/moat/internal/snapshot"
	"github.com/andybons/moat/internal/storage"
	"github.com/spf13/cobra"
)

var (
	snapshotsPruneKeep int
)

var snapshotsCmd = &cobra.Command{
	Use:   "snapshots <run-id>",
	Short: "List snapshots for a run",
	Long: `List all snapshots captured for a specific run.

Snapshots are point-in-time captures of the workspace that can be used
for rollback or analysis. They are created automatically (pre-run) and
can also be created manually or triggered by events (git commits, builds).

Examples:
  moat snapshots run-abc123          # List all snapshots for this run
  moat snapshots run-abc123 --json   # Output as JSON`,
	Args: cobra.ExactArgs(1),
	RunE: listSnapshots,
}

var snapshotsPruneCmd = &cobra.Command{
	Use:   "prune <run-id>",
	Short: "Remove old snapshots, keeping the newest N",
	Long: `Remove old snapshots while keeping the most recent ones.

The pre-run snapshot is always preserved regardless of the --keep value.
This ensures you can always roll back to the original workspace state.

Examples:
  moat snapshots prune run-abc123            # Keep 5 most recent (default)
  moat snapshots prune run-abc123 --keep=3   # Keep 3 most recent
  moat snapshots prune run-abc123 --dry-run  # Show what would be deleted`,
	Args: cobra.ExactArgs(1),
	RunE: pruneSnapshots,
}

func init() {
	rootCmd.AddCommand(snapshotsCmd)
	snapshotsCmd.AddCommand(snapshotsPruneCmd)
	snapshotsPruneCmd.Flags().IntVar(&snapshotsPruneKeep, "keep", 5, "number of snapshots to keep (excluding pre-run)")
}

func listSnapshots(cmd *cobra.Command, args []string) error {
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
	if _, err := os.Stat(snapshotDir); os.IsNotExist(err) {
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
	if _, err := os.Stat(snapshotDir); os.IsNotExist(err) {
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
	if len(otherSnapshots) > snapshotsPruneKeep {
		toDelete = otherSnapshots[snapshotsPruneKeep:]
	}

	if len(toDelete) == 0 {
		fmt.Printf("Nothing to prune (have %d snapshots, keeping %d + %d pre-run)\n",
			len(otherSnapshots), snapshotsPruneKeep, len(preRunSnapshots))
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
	} else {
		fmt.Printf("\nPruned %d snapshots\n", deleted)
	}

	return nil
}
