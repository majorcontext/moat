package cli

import (
	"encoding/json"
	"fmt"

	"github.com/andybons/agentops/internal/log"
	"github.com/andybons/agentops/internal/storage"
	"github.com/spf13/cobra"
)

var (
	traceNetwork bool
	traceVerbose bool
)

var traceCmd = &cobra.Command{
	Use:   "trace [run-id]",
	Short: "View trace spans from a run",
	Long: `View trace spans from a run. If no run-id is specified, shows traces from the most recent run.

Examples:
  agent trace                   # Traces from most recent run
  agent trace run-abc123        # Traces from specific run
  agent trace --network         # Show network requests
  agent trace --network -v      # Show network requests with headers and bodies
  agent trace --json            # Output as JSON`,
	Args: cobra.MaximumNArgs(1),
	RunE: runTrace,
}

func init() {
	rootCmd.AddCommand(traceCmd)
	traceCmd.Flags().BoolVar(&traceNetwork, "network", false, "show network requests")
	traceCmd.Flags().BoolVarP(&traceVerbose, "verbose", "v", false, "show headers and bodies (requires --network)")
}

func runTrace(cmd *cobra.Command, args []string) error {
	baseDir := storage.DefaultBaseDir()

	var runID string
	if len(args) > 0 {
		runID = args[0]
	} else {
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

	if traceNetwork {
		return showNetworkRequests(store, runID)
	}

	return showSpans(store, runID)
}

func showSpans(store *storage.RunStore, runID string) error {
	spans, err := store.ReadSpans()
	if err != nil {
		return fmt.Errorf("reading spans: %w", err)
	}

	if jsonOut {
		data, _ := json.MarshalIndent(spans, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	log.Info("displaying trace spans", "runID", runID)
	if len(spans) == 0 {
		fmt.Println("No spans recorded")
		return nil
	}

	for i, span := range spans {
		duration := span.EndTime.Sub(span.StartTime)
		fmt.Printf("%d. %s (%s)\n", i+1, span.Name, duration)
		if span.ParentID != "" {
			fmt.Printf("   Parent: %s\n", span.ParentID)
		}
		if len(span.Attributes) > 0 {
			for k, v := range span.Attributes {
				fmt.Printf("   %s: %v\n", k, v)
			}
		}
	}
	return nil
}

func showNetworkRequests(store *storage.RunStore, runID string) error {
	reqs, err := store.ReadNetworkRequests()
	if err != nil {
		return fmt.Errorf("reading network requests: %w", err)
	}

	if jsonOut {
		data, _ := json.MarshalIndent(reqs, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	log.Info("displaying network requests", "runID", runID)
	if len(reqs) == 0 {
		fmt.Println("No network requests recorded")
		return nil
	}

	for _, req := range reqs {
		status := fmt.Sprintf("%d", req.StatusCode)
		if req.Error != "" {
			status = "ERR"
		}
		fmt.Printf("[%s] %s %s %s (%dms)\n", req.Timestamp.Format("15:04:05.000"), req.Method, req.URL, status, req.Duration)

		if traceVerbose {
			printHeadersAndBody("Request", req.RequestHeaders, req.RequestBody)
			printHeadersAndBody("Response", req.ResponseHeaders, req.ResponseBody)
			if req.BodyTruncated {
				fmt.Println("  [Body truncated to 8KB in storage]")
			}
			fmt.Println()
		}
	}
	return nil
}

func printHeadersAndBody(label string, headers map[string]string, body string) {
	if len(headers) > 0 {
		fmt.Printf("  %s Headers:\n", label)
		for k, v := range headers {
			fmt.Printf("    %s: %s\n", k, v)
		}
	}
	if body != "" {
		fmt.Printf("  %s Body:\n", label)
		fmt.Printf("    %s\n", truncateDisplay(body, 500))
	}
}

// truncateDisplay truncates a string for display, adding ellipsis if needed.
func truncateDisplay(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
