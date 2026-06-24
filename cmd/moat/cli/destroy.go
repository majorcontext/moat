package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/snapshot"
	"github.com/majorcontext/moat/internal/storage"
	"github.com/spf13/cobra"
)

var destroyForce bool

var destroyCmd = &cobra.Command{
	Use:   "destroy [run]",
	Short: "Destroy a run and its resources",
	Long: `Remove a run and clean up its container and resources.

Accepts a run ID or name. The run must be stopped before it can be destroyed.
If a name matches multiple runs, you'll be prompted to confirm.

For volume-mode runs, the workspace lives only in a Docker volume. Destroying
such a run deletes that volume and loses all agent changes unless an extraction
snapshot was captured first. The command refuses to destroy a volume-mode run
with no extraction snapshot; pass --force to override.`,
	Args: cobra.MaximumNArgs(1),
	RunE: destroyRun,
}

func init() {
	rootCmd.AddCommand(destroyCmd)
	destroyCmd.Flags().BoolVarP(&destroyForce, "force", "f", false, "force destroy even if a volume-mode run has no extraction snapshot")
}

// hasExtractionSnapshot reports whether the run has at least one snapshot that
// is not an automatic/internal type (pre-run or safety). Such a snapshot is the
// user's captured copy of the agent's work. On any error (missing snapshot dir,
// listing failure) it returns false so the destroy guard requires --force, which
// is the safer default.
func hasExtractionSnapshot(runID string) bool {
	baseDir := storage.DefaultBaseDir()
	runDir := filepath.Join(baseDir, runID)
	snapshotDir := filepath.Join(runDir, "snapshots")

	if _, statErr := os.Stat(snapshotDir); statErr != nil {
		return false
	}

	store, err := storage.NewRunStore(baseDir, runID)
	if err != nil {
		return false
	}
	meta, err := store.LoadMetadata()
	if err != nil {
		return false
	}

	engine, err := snapshot.NewEngine(meta.Workspace, snapshotDir, snapshot.EngineOptions{})
	if err != nil {
		return false
	}
	snapshots, err := engine.List()
	if err != nil {
		return false
	}

	for _, s := range snapshots {
		if s.Type != snapshot.TypePreRun && s.Type != snapshot.TypeSafety {
			return true
		}
	}
	return false
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
		// Guard against silent data loss: a volume-mode run with no extraction
		// snapshot only exists inside its Docker volume, which Destroy removes.
		store, storeErr := storage.NewRunStore(storage.DefaultBaseDir(), runID)
		if storeErr != nil {
			return fmt.Errorf("opening run storage for %s: %w", runID, storeErr)
		}
		meta, metaErr := store.LoadMetadata()
		if metaErr != nil {
			return fmt.Errorf("loading metadata for %s: %w", runID, metaErr)
		}
		if guardErr := run.CheckDestroyAllowed(meta.WorkspaceMode, hasExtractionSnapshot(runID), destroyForce); guardErr != nil {
			return guardErr
		}

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
