package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/ui"
	"github.com/majorcontext/moat/internal/worktree"
	"github.com/spf13/cobra"
)

var (
	cleanForce bool
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove stopped runs, unused images, and worktree directories",
	Long: `Interactively remove stopped runs, unused moat images, and worktree
directories associated with stopped runs.

Worktree cleanup prunes git metadata for the current repository only.
Worktrees created from other repos will have their directories removed,
but you may need to run 'git worktree prune' in those repos separately.

Shows what will be removed and asks for confirmation before proceeding.
Use --force to skip confirmation (for scripts).
Use --dry-run to see what would be removed without prompting.`,
	RunE: cleanResources,
}

func init() {
	rootCmd.AddCommand(cleanCmd)
	cleanCmd.Flags().BoolVarP(&cleanForce, "force", "f", false, "skip confirmation prompt")
}

func cleanResources(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Get runtime (no sandbox needed for listing/cleaning)
	rt, err := container.NewRuntimeWithOptions(container.RuntimeOptions{Sandbox: false})
	if err != nil {
		return fmt.Errorf("initializing runtime: %w", err)
	}
	defer rt.Close()

	// Get runs (no sandbox needed for listing/cleaning)
	noSandbox := true
	manager, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &noSandbox})
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	fmt.Println("Scanning for resources to clean...")
	fmt.Println()

	runs := manager.List()

	// Find stopped runs (and track which have worktrees)
	var stoppedRuns []*run.Run
	var worktreeRuns []*run.Run
	for _, r := range runs {
		if r.GetState() == run.StateStopped {
			stoppedRuns = append(stoppedRuns, r)
			if r.WorktreePath != "" {
				worktreeRuns = append(worktreeRuns, r)
			}
		}
	}

	// Find unused images (images not used by any running container)
	// For now, we consider all moat images as candidates for cleanup.
	// A more sophisticated approach would track which images are in use,
	// but this is complex: we'd need to inspect each container's image tag.
	images, err := rt.ListImages(ctx)
	if err != nil {
		return fmt.Errorf("listing images: %w", err)
	}

	// Build a tag-to-ID mapping so we can track images by both identifiers.
	// This handles cases where the same image ID has multiple tags.
	tagToID := make(map[string]string)
	for _, img := range images {
		tagToID[img.Tag] = img.ID
	}

	// Filter out images that might be in use by running containers.
	// Track both tags and IDs since containers report tags but we need
	// to match against image IDs for correctness.
	containers, containerErr := rt.ListContainers(ctx)
	runningImages := make(map[string]bool)
	if containerErr == nil {
		for _, c := range containers {
			if c.Status == "running" {
				runningImages[c.Image] = true
				// Also mark the image ID as in-use if we can resolve it
				if id, ok := tagToID[c.Image]; ok {
					runningImages[id] = true
				}
			}
		}
	} else {
		// Log the error so users know why we might not detect in-use images
		ui.Warnf("Failed to list containers: %v", containerErr)
		ui.Info("Images will be skipped if running containers cannot be verified")
	}

	var unusedImages []container.ImageInfo
	for _, img := range images {
		// Check both tag and ID since an image might be referenced either way
		if !runningImages[img.Tag] && !runningImages[img.ID] {
			unusedImages = append(unusedImages, img)
		}
	}

	// Find orphaned networks (moat-managed networks not associated with any known run)
	var orphanedNetworks []container.NetworkInfo
	netMgr := rt.NetworkManager()
	if netMgr != nil {
		allNetworks, netErr := netMgr.ListNetworks(ctx)
		if netErr != nil {
			ui.Warnf("Failed to list networks: %v", netErr)
		} else {
			// Build set of network IDs associated with known runs
			knownNetworkIDs := make(map[string]bool)
			for _, r := range runs {
				if r.NetworkID != "" {
					knownNetworkIDs[r.NetworkID] = true
				}
			}
			for _, n := range allNetworks {
				if !knownNetworkIDs[n.ID] {
					orphanedNetworks = append(orphanedNetworks, n)
				}
			}
		}
	}

	// Nothing to clean?
	if len(stoppedRuns) == 0 && len(unusedImages) == 0 && len(orphanedNetworks) == 0 {
		fmt.Println("Nothing to clean.")
		return nil
	}

	// Resolve repo root for worktree cleanup (best-effort, may fail if not in a git repo).
	// This only prunes worktrees belonging to the current repository; worktrees
	// created from other repos will have their directories removed but stale
	// .git/worktrees/ entries in those repos will remain until `git worktree prune`
	// is run there.
	var repoRoot string
	if len(worktreeRuns) > 0 {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			ui.Warnf("Cannot determine working directory: %v", cwdErr)
			ui.Info("Worktree cleanup will be skipped")
		} else {
			repoRoot, _ = worktree.FindRepoRoot(cwd)
		}
	}

	// Show what will be removed
	var totalSize int64

	if len(stoppedRuns) > 0 {
		fmt.Printf("%s (%d):\n", ui.Bold("Stopped runs"), len(stoppedRuns))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for _, r := range stoppedRuns {
			age := formatAge(r.CreatedAt)
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", r.Name, r.ID, "stopped", age)
		}
		w.Flush()
		fmt.Println()
	}

	if len(unusedImages) > 0 {
		fmt.Printf("%s (%d):\n", ui.Bold("Unused images"), len(unusedImages))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for _, img := range unusedImages {
			sizeMB := img.Size / (1024 * 1024)
			totalSize += img.Size
			fmt.Fprintf(w, "  %s\t%s\t%d MB\n", img.Tag, formatAge(img.Created), sizeMB)
		}
		w.Flush()
		fmt.Println()
	}

	if len(orphanedNetworks) > 0 {
		fmt.Printf("%s (%d):\n", ui.Bold("Orphaned networks"), len(orphanedNetworks))
		for _, n := range orphanedNetworks {
			fmt.Printf("  %s\n", n.Name)
		}
		fmt.Println()
	}

	if len(worktreeRuns) > 0 {
		fmt.Printf("%s (%d):\n", ui.Bold("Worktree directories"), len(worktreeRuns))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for _, r := range worktreeRuns {
			branch := r.WorktreeBranch
			if branch == "" {
				branch = "(unknown)"
			}
			fmt.Fprintf(w, "  %s\t(%s)\n", branch, r.WorktreePath)
		}
		w.Flush()
		fmt.Println()
	}

	// worktreeRuns is a subset of stoppedRuns, so don't count them separately.
	resourceCount := len(stoppedRuns) + len(unusedImages) + len(orphanedNetworks)
	fmt.Printf("Total: %d resources, %d MB\n\n", resourceCount, totalSize/(1024*1024))

	// Dry run - just show, don't prompt
	if dryRun {
		fmt.Println("Dry run - no changes made")
		return nil
	}

	// Confirm
	if !cleanForce {
		fmt.Print("Remove these resources? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Println("Canceled")
			return nil
		}
		fmt.Println()
	}

	// Remove stopped runs
	var freedSize int64
	var removedCount, failedCount int
	destroyedRunIDs := make(map[string]bool)
	for _, r := range stoppedRuns {
		fmt.Printf("Removing run %s (%s)... ", r.Name, r.ID)
		if err := manager.Destroy(ctx, r.ID); err != nil {
			fmt.Printf("%s\n", ui.Red(fmt.Sprintf("error: %v", err)))
			failedCount++
			continue
		}
		fmt.Println(ui.Green("done"))
		removedCount++
		destroyedRunIDs[r.ID] = true
	}

	// Remove unused images
	// Re-check each image before removal to handle race condition where
	// a container might have started using the image since we listed them.
	for _, img := range unusedImages {
		fmt.Printf("Removing image %s... ", img.Tag)

		// Re-check if image is now in use
		if isImageInUse(ctx, rt, img.ID, img.Tag) {
			fmt.Println(ui.Yellow("skipped (now in use)"))
			continue
		}

		if err := rt.RemoveImage(ctx, img.ID); err != nil {
			fmt.Printf("%s\n", ui.Red(fmt.Sprintf("error: %v", err)))
			failedCount++
			continue
		}
		fmt.Println(ui.Green("done"))
		removedCount++
		freedSize += img.Size
	}

	// Remove orphaned networks
	if netMgr != nil {
		for _, n := range orphanedNetworks {
			fmt.Printf("Removing network %s... ", n.Name)
			if err := netMgr.ForceRemoveNetwork(ctx, n.ID); err != nil {
				fmt.Printf("%s\n", ui.Red(fmt.Sprintf("error: %v", err)))
				failedCount++
				continue
			}
			fmt.Println(ui.Green("done"))
			removedCount++
		}
	}

	// Clean worktree directories only for runs that were successfully destroyed.
	// If destroy failed, the run record still exists and removing its worktree
	// would leave it visible in "moat list" but with a missing directory.
	var wtFailedCount, wtSkippedCount int
	for _, r := range worktreeRuns {
		if !destroyedRunIDs[r.ID] {
			continue
		}
		branch := r.WorktreeBranch
		if branch == "" {
			branch = r.WorktreePath
		}
		fmt.Printf("Removing worktree %s... ", branch)
		root := repoRoot
		if root == "" {
			fmt.Println(ui.Yellow("skipped (not in a git repo)"))
			wtSkippedCount++
			continue
		}
		if err := worktree.Clean(root, r.WorktreePath); err != nil {
			fmt.Printf("%s\n", ui.Red(fmt.Sprintf("error: %v", err)))
			wtFailedCount++
			continue
		}
		// Don't increment removedCount here — worktree cleanup is part of
		// the run removal already counted in the stopped-runs loop above.
		fmt.Println(ui.Green("done"))
	}

	fmt.Printf("\nCleaned %d resources, freed %d MB", removedCount, freedSize/(1024*1024))
	if failedCount > 0 {
		fmt.Printf(" (%d failed)", failedCount)
	}
	if wtFailedCount > 0 {
		fmt.Printf(" (%d worktree cleanups failed)", wtFailedCount)
	}
	if wtSkippedCount > 0 {
		fmt.Printf(" (%d worktree dirs skipped — run from within the repo to clean)", wtSkippedCount)
	}
	fmt.Println()
	return nil
}

// isImageInUse checks if an image is currently being used by any running container.
// This is used to prevent race conditions during image cleanup.
func isImageInUse(ctx context.Context, rt container.Runtime, imageID, imageTag string) bool {
	containers, err := rt.ListContainers(ctx)
	if err != nil {
		// If we can't list containers, err on the side of caution
		return true
	}
	for _, c := range containers {
		if c.Status == "running" && (c.Image == imageTag || c.Image == imageID) {
			return true
		}
	}
	return false
}
