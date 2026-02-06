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
	"github.com/spf13/cobra"
)

var (
	cleanForce bool
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove stopped runs and unused images",
	Long: `Interactively remove stopped runs and unused moat images.

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

	// Find stopped runs
	var stoppedRuns []*run.Run
	for _, r := range runs {
		if r.State == run.StateStopped {
			stoppedRuns = append(stoppedRuns, r)
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
		ui.Info("Proceeding with image cleanup but cannot verify images are unused")
	}

	var unusedImages []container.ImageInfo
	for _, img := range images {
		// Check both tag and ID since an image might be referenced either way
		if !runningImages[img.Tag] && !runningImages[img.ID] {
			unusedImages = append(unusedImages, img)
		}
	}

	// Nothing to clean?
	if len(stoppedRuns) == 0 && len(unusedImages) == 0 {
		fmt.Println("Nothing to clean.")
		return nil
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

	resourceCount := len(stoppedRuns) + len(unusedImages)
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
	for _, r := range stoppedRuns {
		fmt.Printf("Removing run %s (%s)... ", r.Name, r.ID)
		if err := manager.Destroy(ctx, r.ID); err != nil {
			fmt.Printf("%s\n", ui.Red(fmt.Sprintf("error: %v", err)))
			failedCount++
			continue
		}
		fmt.Println(ui.Green("done"))
		removedCount++
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

	if failedCount > 0 {
		fmt.Printf("\nCleaned %d resources, freed %d MB (%d failed)\n", removedCount, freedSize/(1024*1024), failedCount)
	} else {
		fmt.Printf("\nCleaned %d resources, freed %d MB\n", removedCount, freedSize/(1024*1024))
	}
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
