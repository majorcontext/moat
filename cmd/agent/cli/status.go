package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/andybons/agentops/internal/container"
	"github.com/andybons/agentops/internal/run"
	"github.com/andybons/agentops/internal/storage"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show runs, images, disk usage, and health",
	Long: `Display the current state of agentops resources including:
- Active and stopped runs
- Cached container images
- Disk usage
- Health indicators and cleanup suggestions`,
	RunE: showStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

type statusOutput struct {
	Runtime   string       `json:"runtime"`
	Runs      []runInfo    `json:"runs"`
	Images    []imageInfo  `json:"images"`
	Health    []healthItem `json:"health"`
	TotalDisk int64        `json:"total_disk_bytes"`
}

type runInfo struct {
	Name   string `json:"name"`
	ID     string `json:"id"`
	State  string `json:"state"`
	Age    string `json:"age"`
	DiskMB int64  `json:"disk_mb"`
}

type imageInfo struct {
	Tag     string `json:"tag"`
	Created string `json:"created"`
	SizeMB  int64  `json:"size_mb"`
}

type healthItem struct {
	Status  string `json:"status"` // "ok", "warning"
	Message string `json:"message"`
}

func showStatus(cmd *cobra.Command, args []string) error {
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

	runs := manager.List()

	// Get images
	images, err := rt.ListImages(ctx)
	if err != nil {
		return fmt.Errorf("listing images: %w", err)
	}

	// Calculate disk usage per run
	runDiskUsage := make(map[string]int64)
	baseDir := storage.DefaultBaseDir()
	for _, r := range runs {
		runDir := filepath.Join(baseDir, r.ID)
		size := getDirSize(runDir)
		runDiskUsage[r.ID] = size
	}

	// Build output
	output := statusOutput{
		Runtime: string(rt.Type()),
	}

	// Runs section
	var runningCount, stoppedCount int
	var stoppedDisk int64
	for _, r := range runs {
		age := formatAge(r.CreatedAt)
		diskMB := runDiskUsage[r.ID] / (1024 * 1024)
		output.Runs = append(output.Runs, runInfo{
			Name:   r.Name,
			ID:     r.ID,
			State:  string(r.State),
			Age:    age,
			DiskMB: diskMB,
		})
		if r.State == run.StateRunning {
			runningCount++
		} else {
			stoppedCount++
			stoppedDisk += runDiskUsage[r.ID]
		}
	}
	stoppedDiskMB := stoppedDisk / (1024 * 1024)

	// Images section
	var totalImageSize int64
	for _, img := range images {
		sizeMB := img.Size / (1024 * 1024)
		totalImageSize += img.Size
		output.Images = append(output.Images, imageInfo{
			Tag:     img.Tag,
			Created: formatAge(img.Created),
			SizeMB:  sizeMB,
		})
	}

	// Health section
	if stoppedCount > 0 {
		output.Health = append(output.Health, healthItem{
			Status:  "warning",
			Message: fmt.Sprintf("%d stopped runs can be cleaned (%d MB)", stoppedCount, stoppedDiskMB),
		})
	}

	// Check for orphaned containers and unused images
	containers, err := rt.ListContainers(ctx)
	if err != nil {
		// Log warning but don't fail - this is just a health check
		fmt.Fprintf(os.Stderr, "Warning: failed to list containers: %v\n", err)
	} else {
		knownRunIDs := make(map[string]bool)
		runningImages := make(map[string]bool)
		for _, r := range runs {
			knownRunIDs[r.ID] = true
		}
		orphanedCount := 0
		for _, c := range containers {
			if !knownRunIDs[c.Name] {
				orphanedCount++
			}
			if c.Status == "running" {
				runningImages[c.Image] = true
			}
		}
		if orphanedCount > 0 {
			output.Health = append(output.Health, healthItem{
				Status:  "warning",
				Message: fmt.Sprintf("%d orphaned containers", orphanedCount),
			})
		} else {
			output.Health = append(output.Health, healthItem{
				Status:  "ok",
				Message: "No orphaned containers",
			})
		}

		// Check for unused images (not used by any running container)
		// Check both tag and ID since containers might report either
		unusedImageCount := 0
		for _, img := range images {
			if !runningImages[img.Tag] && !runningImages[img.ID] {
				unusedImageCount++
			}
		}
		if unusedImageCount > 0 {
			output.Health = append(output.Health, healthItem{
				Status:  "warning",
				Message: fmt.Sprintf("%d unused images can be cleaned", unusedImageCount),
			})
		} else if len(images) > 0 {
			output.Health = append(output.Health, healthItem{
				Status:  "ok",
				Message: "No unused images",
			})
		}
	}

	output.TotalDisk = totalImageSize
	for _, size := range runDiskUsage {
		output.TotalDisk += size
	}

	// Output
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(output)
	}

	// Human-readable output
	fmt.Printf("Runtime: %s\n\n", output.Runtime)

	// Runs table
	if len(output.Runs) == 0 {
		fmt.Println("Runs (0)")
	} else {
		fmt.Printf("Runs (%d total, %d running)\n", len(output.Runs), runningCount)
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  NAME\tRUN ID\tSTATE\tAGE\tDISK")
		for _, r := range output.Runs {
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%d MB\n",
				r.Name, r.ID, r.State, r.Age, r.DiskMB)
		}
		w.Flush()
	}
	fmt.Println()

	// Images table
	if len(output.Images) == 0 {
		fmt.Println("Images (0)")
	} else {
		fmt.Printf("Images (%d total, %d MB)\n", len(output.Images), totalImageSize/(1024*1024))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  TAG\tCREATED\tSIZE")
		for _, img := range output.Images {
			fmt.Fprintf(w, "  %s\t%s\t%d MB\n",
				img.Tag, img.Created, img.SizeMB)
		}
		w.Flush()
	}
	fmt.Println()

	// Health section
	fmt.Println("Health")
	for _, h := range output.Health {
		icon := "✓"
		if h.Status == "warning" {
			icon = "⚠"
		}
		fmt.Printf("  %s %s\n", icon, h.Message)
	}

	return nil
}

func formatAge(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func getDirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}
