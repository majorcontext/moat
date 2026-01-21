package cli

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/andybons/moat/internal/log"
	"github.com/andybons/moat/internal/storage"
	"github.com/spf13/cobra"
)

var (
	logsFollow bool
	logsLines  int
)

var logsCmd = &cobra.Command{
	Use:   "logs [run-id]",
	Short: "View logs from a run",
	Long: `View logs from a run. If no run-id is specified, shows logs from the most recent run.

Examples:
  moat logs                    # Logs from most recent run
  moat logs run_a1b2c3d4e5f6       # Logs from specific run
  moat logs -f                 # Follow logs (like tail -f)
  moat logs -n 50              # Show last 50 lines`,
	Args: cobra.MaximumNArgs(1),
	RunE: runLogs,
}

func init() {
	rootCmd.AddCommand(logsCmd)
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "follow log output")
	logsCmd.Flags().IntVarP(&logsLines, "lines", "n", 100, "number of lines to show")
}

func runLogs(cmd *cobra.Command, args []string) error {
	baseDir := storage.DefaultBaseDir()

	var runID string
	if len(args) > 0 {
		runID = args[0]
	} else {
		// Find most recent run
		var err error
		runID, err = findLatestRun(baseDir)
		if err != nil {
			return err
		}
	}

	store, err := storage.NewRunStore(baseDir, runID)
	if err != nil {
		return fmt.Errorf("opening run storage: %w", err)
	}

	entries, err := store.ReadLogs(0, logsLines)
	if err != nil {
		return fmt.Errorf("reading logs: %w", err)
	}

	log.Info("displaying logs", "runID", runID)
	for _, entry := range entries {
		ts := entry.Timestamp.Format("15:04:05.000")
		fmt.Printf("[%s] %s\n", ts, entry.Line)
	}

	if logsFollow {
		log.Info("Follow mode not yet implemented")
	}

	return nil
}

// findLatestRun finds the most recently modified run directory.
func findLatestRun(baseDir string) (string, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no runs found (directory does not exist)")
		}
		return "", fmt.Errorf("reading runs dir: %w", err)
	}

	if len(entries) == 0 {
		return "", fmt.Errorf("no runs found")
	}

	// Sort by modification time (newest first)
	type runInfo struct {
		name    string
		modTime time.Time
	}
	runs := make([]runInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		runs = append(runs, runInfo{name: e.Name(), modTime: info.ModTime()})
	}

	sort.Slice(runs, func(i, j int) bool {
		return runs[i].modTime.After(runs[j].modTime)
	})

	if len(runs) == 0 {
		return "", fmt.Errorf("no runs found")
	}

	return runs[0].name, nil
}
