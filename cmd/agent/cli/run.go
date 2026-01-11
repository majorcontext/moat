package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/andybons/agentops/internal/log"
	"github.com/andybons/agentops/internal/run"
	"github.com/spf13/cobra"
)

var (
	grants []string
)

var runCmd = &cobra.Command{
	Use:   "run <agent> [path] [-- command]",
	Short: "Run an agent in an isolated environment",
	Long: `Run an agent in an isolated, ephemeral workspace.

Examples:
  agent run claude-code .
  agent run claude-code ./my-repo --grant github,aws:s3.read
  agent run test . -- ls -la
  agent run test . -- echo "Hello from container"`,
	Args: cobra.MinimumNArgs(1),
	RunE: runAgent,
}

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().StringSliceVar(&grants, "grant", nil, "capabilities to grant (e.g., github, aws:s3.read)")
}

func runAgent(cmd *cobra.Command, args []string) error {
	agentName := args[0]

	// Parse args: <agent> [path] [-- command...]
	workspacePath := "."
	var containerCmd []string

	// Check if there's a -- separator
	dashIdx := cmd.ArgsLenAtDash()
	if dashIdx >= 0 {
		// Args before -- are agent and path
		if dashIdx > 1 {
			workspacePath = args[1]
		}
		// Args after -- are the command
		containerCmd = args[dashIdx:]
	} else {
		// No --, so second arg (if present) is path
		if len(args) > 1 {
			workspacePath = args[1]
		}
	}

	// Resolve to absolute path
	absPath, err := filepath.Abs(workspacePath)
	if err != nil {
		return fmt.Errorf("resolving workspace path: %w", err)
	}

	// Verify path exists
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("workspace path %q: %w", absPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace path %q is not a directory", absPath)
	}

	log.Debug("preparing run",
		"agent", agentName,
		"workspace", absPath,
		"grants", grants,
		"cmd", containerCmd,
	)

	if dryRun {
		fmt.Println("Dry run - would start agent container")
		if len(containerCmd) > 0 {
			fmt.Printf("Command: %v\n", containerCmd)
		}
		return nil
	}

	// Set up context with signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	// Create manager
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	opts := run.Options{
		Agent:     agentName,
		Workspace: absPath,
		Grants:    grants,
		Cmd:       containerCmd,
	}

	// Create run
	r, err := manager.Create(ctx, opts)
	if err != nil {
		return fmt.Errorf("creating run: %w", err)
	}

	log.Info("created run", "id", r.ID, "agent", agentName)
	fmt.Printf("Created run: %s\n", r.ID)

	// Start run
	if err := manager.Start(ctx, r.ID); err != nil {
		log.Error("failed to start run", "id", r.ID, "error", err)
		return fmt.Errorf("starting run: %w", err)
	}

	log.Info("run started", "id", r.ID)
	fmt.Printf("Run %s started\n", r.ID)

	// Wait for completion or interrupt
	if err := manager.Wait(ctx, r.ID); err != nil {
		if ctx.Err() != nil {
			// Context was cancelled (signal received)
			log.Info("run stopped by user", "id", r.ID)
			fmt.Printf("\nRun %s stopped\n", r.ID)
			return nil
		}
		log.Error("run failed", "id", r.ID, "error", err)
		return err
	}

	log.Info("run completed", "id", r.ID)
	fmt.Printf("Run %s completed\n", r.ID)
	return nil
}
