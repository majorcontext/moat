package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/storage"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show system status summary",
	Long: `Display a high-level summary of moat resources including:
- Active runs
- Totals for stopped runs and images
- Health indicators

For detailed information, use:
  moat list              List all runs
  moat system images     List all images
  moat system containers List all containers`,
	RunE: showStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

type statusOutput struct {
	Runtime    string       `json:"runtime"`
	ActiveRuns []runInfo    `json:"active_runs"`
	Images     []imageInfo  `json:"images"`
	Health     []healthItem `json:"health"`
	TotalDisk  int64        `json:"total_disk_bytes"`
}

type runInfo struct {
	Name      string `json:"name"`
	ID        string `json:"id"`
	State     string `json:"state"`
	Age       string `json:"age"`
	DiskMB    int64  `json:"disk_mb"`
	Endpoints string `json:"endpoints,omitempty"`
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

	// Get runtime (no sandbox needed for status queries)
	rt, err := container.NewRuntimeWithOptions(container.RuntimeOptions{Sandbox: false})
	if err != nil {
		return fmt.Errorf("initializing runtime: %w", err)
	}
	defer rt.Close()

	// Get runs (no sandbox needed for status queries)
	noSandbox := true
	manager, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &noSandbox})
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	runs := manager.List()

	// Sort runs by age (newest first)
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})

	// Get images
	images, err := rt.ListImages(ctx)
	if err != nil {
		return fmt.Errorf("listing images: %w", err)
	}

	// Calculate disk usage only for active runs (with timeout to prevent blocking on slow disks)
	runDiskUsage := make(map[string]int64)
	baseDir := storage.DefaultBaseDir()
	var activeRuns []*run.Run
	var stoppedCount int
	var stoppedDisk int64

	for _, r := range runs {
		runDir := filepath.Join(baseDir, r.ID)
		size := getDirSizeWithTimeout(runDir, 2*time.Second)

		if r.State == run.StateStopped || r.State == run.StateFailed {
			stoppedCount++
			if size >= 0 {
				stoppedDisk += size
			}
		} else {
			// Include all non-stopped states: created, starting, running, stopping
			activeRuns = append(activeRuns, r)
			runDiskUsage[r.ID] = size
		}
	}

	// Build output
	output := statusOutput{
		Runtime: string(rt.Type()),
	}

	// Active runs section
	for _, r := range activeRuns {
		age := formatAge(r.CreatedAt)
		size := runDiskUsage[r.ID]
		var diskMB int64
		if size >= 0 {
			diskMB = size / (1024 * 1024)
		} else {
			diskMB = -1 // Indicates timeout/unknown
		}
		endpoints := ""
		if len(r.Ports) > 0 {
			names := make([]string, 0, len(r.Ports))
			for name := range r.Ports {
				names = append(names, name)
			}
			endpoints = strings.Join(names, ", ")
		}
		output.ActiveRuns = append(output.ActiveRuns, runInfo{
			Name:      r.Name,
			ID:        r.ID,
			State:     string(r.State),
			Age:       age,
			DiskMB:    diskMB,
			Endpoints: endpoints,
		})
	}

	// Images section - only need count for summary
	var totalImageSize int64
	for _, img := range images {
		totalImageSize += img.Size
		output.Images = append(output.Images, imageInfo{
			Tag:     img.Tag,
			Created: formatAge(img.Created),
			SizeMB:  img.Size / (1024 * 1024),
		})
	}

	// Health section
	if stoppedCount > 0 {
		stoppedDiskMB := stoppedDisk / (1024 * 1024)
		output.Health = append(output.Health, healthItem{
			Status:  "warning",
			Message: fmt.Sprintf("%d stopped runs (%d MB)", stoppedCount, stoppedDiskMB),
		})
	}

	// Check for orphaned containers
	containers, err := rt.ListContainers(ctx)
	if err != nil {
		// Add health warning so users know container checks are incomplete
		output.Health = append(output.Health, healthItem{
			Status:  "warning",
			Message: fmt.Sprintf("Failed to list containers: %v", err),
		})
	} else {
		knownRunIDs := make(map[string]bool)
		for _, r := range runs {
			knownRunIDs[r.ID] = true
		}
		orphanedCount := 0
		for _, c := range containers {
			if !knownRunIDs[c.Name] {
				orphanedCount++
			}
		}
		if orphanedCount > 0 {
			output.Health = append(output.Health, healthItem{
				Status:  "warning",
				Message: fmt.Sprintf("%d orphaned containers", orphanedCount),
			})
		}
	}

	output.TotalDisk = totalImageSize + stoppedDisk
	for _, size := range runDiskUsage {
		if size >= 0 {
			output.TotalDisk += size
		}
	}

	// Output
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(output)
	}

	// Human-readable output
	fmt.Printf("Runtime: %s\n\n", output.Runtime)

	// Active runs table
	if len(activeRuns) == 0 {
		fmt.Println("Active Runs: 0")
	} else {
		fmt.Printf("Active Runs: %d\n", len(activeRuns))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  NAME\tRUN ID\tAGE\tDISK\tENDPOINTS")
		for _, r := range output.ActiveRuns {
			diskStr := fmt.Sprintf("%d MB", r.DiskMB)
			if r.DiskMB < 0 {
				diskStr = "?"
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n",
				r.Name, r.ID, r.Age, diskStr, r.Endpoints)
		}
		w.Flush()
	}
	fmt.Println()

	// Summary statistics
	fmt.Println("Summary")
	stoppedDiskMB := stoppedDisk / (1024 * 1024)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "  Stopped runs:\t%d\t%d MB\n", stoppedCount, stoppedDiskMB)
	fmt.Fprintf(w, "  Images:\t%d\t%d MB\n", len(images), totalImageSize/(1024*1024))
	fmt.Fprintf(w, "  Total disk:\t\t%d MB\n", output.TotalDisk/(1024*1024))
	w.Flush()
	fmt.Println()

	// Health section
	if len(output.Health) > 0 {
		fmt.Println("Health")
		for _, h := range output.Health {
			icon := "⚠"
			if h.Status == "ok" {
				icon = "✓"
			}
			fmt.Printf("  %s %s\n", icon, h.Message)
		}
		fmt.Println()
	}

	// Hints for detailed views
	fmt.Println("For details:")
	fmt.Println("  moat list                List all runs")
	fmt.Println("  moat system images       List all images")
	fmt.Println("  moat system containers   List all containers")

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

// getDirSizeWithTimeout calculates directory size with a timeout to prevent
// blocking on slow filesystems. Returns -1 if the operation times out.
func getDirSizeWithTimeout(path string, timeout time.Duration) int64 {
	result := make(chan int64, 1)
	go func() {
		result <- getDirSize(path)
	}()

	select {
	case size := <-result:
		return size
	case <-time.After(timeout):
		return -1
	}
}

func getDirSize(path string) int64 {
	var size int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
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
