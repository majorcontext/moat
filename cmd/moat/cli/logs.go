package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/storage"
	"github.com/spf13/cobra"
)

var (
	logsFollow bool
	logsLines  int
)

var logsCmd = &cobra.Command{
	Use:   "logs [run]",
	Short: "View logs from a run",
	Long: `View logs from a run. Accepts a run ID or name.
If no argument is specified, shows logs from the most recent run.

Examples:
  moat logs                    # Logs from most recent run
  moat logs my-agent           # Logs from run by name
  moat logs run_a1b2c3d4e5f6   # Logs from specific run
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

	// Use manager to resolve name or ID and check run state
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	var runID string
	if len(args) > 0 {
		runID, err = resolveRunArgSingle(manager, args[0])
		if err != nil {
			return err
		}
	} else {
		// Find most recent run
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
		return followLogs(manager, store, runID, len(entries))
	}

	return nil
}

// followLogs tails the log file, printing new entries as they appear.
// Stops when the run completes or the user presses Ctrl+C.
func followLogs(manager *run.Manager, store *storage.RunStore, runID string, offset int) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Monitor container exit in background
	exitDone := make(chan struct{})
	go func() {
		_ = manager.Wait(ctx, runID)
		close(exitDone)
	}()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			return nil
		case <-exitDone:
			// Container exited - do one final read to catch remaining logs
			entries, _ := store.ReadLogs(offset, 10000)
			for _, entry := range entries {
				ts := entry.Timestamp.Format("15:04:05.000")
				fmt.Printf("[%s] %s\n", ts, entry.Line)
			}
			return nil
		case <-ticker.C:
			entries, err := store.ReadLogs(offset, 1000)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				ts := entry.Timestamp.Format("15:04:05.000")
				fmt.Printf("[%s] %s\n", ts, entry.Line)
				offset++
			}
		}
	}
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
