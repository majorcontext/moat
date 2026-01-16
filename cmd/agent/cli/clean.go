package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/andybons/agentops/internal/container"
	"github.com/andybons/agentops/internal/run"
	"github.com/spf13/cobra"
)

var (
	cleanForce bool
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove stopped runs and unused images",
	Long: `Interactively remove stopped runs and unused agentops images.

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

	// Get runtime
	rt, err := container.NewRuntime()
	if err != nil {
		return fmt.Errorf("initializing runtime: %w", err)
	}
	defer rt.Close()

	// Get runs
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	fmt.Println("Scanning for resources to clean...")
	fmt.Println()

	runs := manager.List()

	// Find stopped runs
	var stoppedRuns []*run.Run
	runningImages := make(map[string]bool)
	for _, r := range runs {
		if r.State == run.StateRunning {
			// Track images used by running containers
			// TODO: get actual image tag from container
		} else if r.State == run.StateStopped {
			stoppedRuns = append(stoppedRuns, r)
		}
	}

	// Find unused images
	images, err := rt.ListImages(ctx)
	if err != nil {
		return fmt.Errorf("listing images: %w", err)
	}

	var unusedImages []container.ImageInfo
	for _, img := range images {
		if !runningImages[img.Tag] {
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
		fmt.Printf("Stopped runs (%d):\n", len(stoppedRuns))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for _, r := range stoppedRuns {
			age := formatAge(r.CreatedAt)
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", r.Name, r.ID, "stopped", age)
		}
		w.Flush()
		fmt.Println()
	}

	if len(unusedImages) > 0 {
		fmt.Printf("Unused images (%d):\n", len(unusedImages))
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
			fmt.Println("Cancelled")
			return nil
		}
		fmt.Println()
	}

	// Remove stopped runs
	var freedSize int64
	var removedCount int
	for _, r := range stoppedRuns {
		fmt.Printf("Removing run %s (%s)... ", r.Name, r.ID)
		if err := manager.Destroy(ctx, r.ID); err != nil {
			fmt.Printf("error: %v\n", err)
			continue
		}
		fmt.Println("done")
		removedCount++
	}

	// Remove unused images
	for _, img := range unusedImages {
		fmt.Printf("Removing image %s... ", img.Tag)
		if err := rt.RemoveImage(ctx, img.ID); err != nil {
			fmt.Printf("error: %v\n", err)
			continue
		}
		fmt.Println("done")
		removedCount++
		freedSize += img.Size
	}

	fmt.Printf("\nCleaned %d resources, freed %d MB\n", removedCount, freedSize/(1024*1024))
	return nil
}
