package cli

import (
	"fmt"
	"time"

	"github.com/majorcontext/moat/internal/trace"
	"github.com/spf13/cobra"
)

var ttyTraceCmd = &cobra.Command{
	Use:   "tty-trace",
	Short: "Capture and analyze terminal I/O for TUI debugging",
	Long: `Capture, replay, and analyze terminal I/O for debugging TUI rendering issues.

Use --tty-trace flag with 'moat claude' or 'moat run -i' to capture traces:
  moat claude --tty-trace=session.json

Then analyze the trace:
  moat tty-trace analyze session.json --decode
  moat tty-trace analyze session.json --find-resize-issues`,
}

var ttyTraceAnalyzeCmd = &cobra.Command{
	Use:   "analyze <trace-file>",
	Short: "Analyze a terminal I/O trace file",
	Args:  cobra.ExactArgs(1),
	RunE:  runTTYTraceAnalyze,
}

var (
	ttyDecodeFlag     bool
	ttyClearFlag      bool
	ttyResizeFlag     bool
	ttyResizeWindowMs int
)

func init() {
	rootCmd.AddCommand(ttyTraceCmd)
	ttyTraceCmd.AddCommand(ttyTraceAnalyzeCmd)

	ttyTraceAnalyzeCmd.Flags().BoolVar(&ttyDecodeFlag, "decode", false, "decode and display all control sequences")
	ttyTraceAnalyzeCmd.Flags().BoolVar(&ttyClearFlag, "find-clears", false, "find screen clear operations")
	ttyTraceAnalyzeCmd.Flags().BoolVar(&ttyResizeFlag, "find-resize-issues", false, "find potential resize timing issues")
	ttyTraceAnalyzeCmd.Flags().IntVar(&ttyResizeWindowMs, "resize-window", 100, "time window in ms for resize issue detection")
}

func runTTYTraceAnalyze(cmd *cobra.Command, args []string) error {
	tracePath := args[0]

	t, err := trace.Load(tracePath)
	if err != nil {
		return fmt.Errorf("loading trace: %w", err)
	}

	// Show metadata
	fmt.Printf("=== Trace Metadata ===\n")
	fmt.Printf("Run ID: %s\n", t.Metadata.RunID)
	fmt.Printf("Command: %v\n", t.Metadata.Command)
	fmt.Printf("Captured: %s\n", t.Metadata.Timestamp.Format(time.RFC3339))
	fmt.Printf("Initial size: %dx%d\n", t.Metadata.InitialSize.Width, t.Metadata.InitialSize.Height)
	fmt.Printf("Environment:\n")
	for k, v := range t.Metadata.Environment {
		fmt.Printf("  %s=%s\n", k, v)
	}
	fmt.Printf("Total events: %d\n\n", len(t.Events))

	// Count event types
	counts := make(map[trace.EventType]int)
	for _, e := range t.Events {
		counts[e.Type]++
	}
	fmt.Printf("Event breakdown:\n")
	for typ, count := range counts {
		fmt.Printf("  %s: %d\n", typ, count)
	}
	fmt.Println()

	// Find clear screen operations
	if ttyClearFlag || (!ttyDecodeFlag && !ttyResizeFlag) {
		clearIndices := t.FindClearScreen()
		if len(clearIndices) > 0 {
			fmt.Printf("=== Screen Clears (%d found) ===\n", len(clearIndices))
			for _, idx := range clearIndices {
				ts := float64(t.Events[idx].TimestampNano) / 1e9
				fmt.Printf("  Event %d at %.3fs\n", idx, ts)
			}
			fmt.Println()
		} else {
			fmt.Println("No screen clears found")
		}
	}

	// Find resize issues
	if ttyResizeFlag || (!ttyDecodeFlag && !ttyClearFlag) {
		windowNanos := int64(ttyResizeWindowMs) * 1000 * 1000
		issues := t.FindResizeIssues(windowNanos)
		if len(issues) > 0 {
			fmt.Printf("=== Potential Resize Timing Issues ===\n")
			fmt.Printf("(looking for screen clears within %dms of resize)\n\n", ttyResizeWindowMs)
			for _, issue := range issues {
				fmt.Printf("  ⚠ %s\n", issue)
			}
			fmt.Println()
		} else {
			fmt.Printf("No resize timing issues found (window=%dms)\n\n", ttyResizeWindowMs)
		}
	}

	// Decode events
	if ttyDecodeFlag {
		fmt.Println("=== Decoded Events ===")
		decoded := t.Decode()
		for i, de := range decoded {
			ts := float64(de.TimestampNano) / 1e9
			fmt.Printf("%6d [%8.3fs] [%-6s] %s\n", i, ts, de.Type, de.Decoded)
		}
	}

	return nil
}
