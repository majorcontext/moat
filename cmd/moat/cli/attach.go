package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/run"
	"github.com/spf13/cobra"
)

// Timing constants for attach behavior
const (
	// doublePressWindow is how quickly Ctrl+C must be pressed twice to stop a run
	doublePressWindow = 500 * time.Millisecond
	// containerExitCheckDelay is how long to wait for container exit detection
	containerExitCheckDelay = 200 * time.Millisecond
	// ttyStartupDelay is how long to wait before resizing TTY after container starts
	// This allows the container process to initialize before we resize.
	ttyStartupDelay = 200 * time.Millisecond
)

var (
	attachInteractive bool
	attachTTYTrace    string
)

var attachCmd = &cobra.Command{
	Use:   "attach <run>",
	Short: "Attach to a running agent",
	Long: `Attach local stdin, stdout, and stderr to a running agent.

Accepts a run ID or name. If a name matches multiple runs, you must
specify the run ID.

By default, attaches in the same mode the run was started with:
  - If the run was started with -i, attach will use interactive mode
  - Otherwise, only stdout/stderr are connected

Use -i to force interactive mode, or -i=false to force output-only mode.

Non-interactive mode:
  Ctrl+C            Detach (run continues)
  Ctrl+C Ctrl+C     Stop the run (within 500ms)

Interactive mode (-i):
  Ctrl-/ d          Detach (run continues)
  Ctrl-/ k          Stop the run
  Ctrl+C            Sent to container process

Examples:
  # Attach by name or ID
  moat attach my-agent
  moat attach run_a1b2c3d4e5f6

  # Force interactive mode
  moat attach -i run_a1b2c3d4e5f6

  # Force output-only mode even if run was started interactively
  moat attach -i=false run_a1b2c3d4e5f6`,
	Args: cobra.ExactArgs(1),
	RunE: attachToRun,
}

func init() {
	rootCmd.AddCommand(attachCmd)
	attachCmd.Flags().BoolVarP(&attachInteractive, "interactive", "i", false, "interactive mode (use -i=false to force output-only)")
	attachCmd.Flags().StringVar(&attachTTYTrace, "tty-trace", "", "capture terminal I/O to file for debugging (e.g., session.json)")
}

func attachToRun(cmd *cobra.Command, args []string) error {
	// Create manager
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	// Resolve argument to a single run
	runID, err := resolveRunArgSingle(manager, args[0])
	if err != nil {
		return err
	}

	// Verify run exists and is running
	r, err := manager.Get(runID)
	if err != nil {
		return fmt.Errorf("run not found: %w", err)
	}

	if state := r.GetState(); state != run.StateRunning {
		return fmt.Errorf("run %s is not running (state: %s)", runID, state)
	}

	// Determine interactive mode:
	// - If -i flag was explicitly set, use that value
	// - Otherwise, use the run's original mode
	interactive := r.Interactive // Default to run's mode
	if cmd.Flags().Changed("interactive") {
		interactive = attachInteractive
	}

	fmt.Printf("Attaching to run %s (%s)...\n", r.ID, r.Name)
	if interactive {
		fmt.Printf("Escape: Ctrl-/ d (detach), Ctrl-/ k (stop). Ctrl+C goes to container.\n")
	} else {
		fmt.Println("Press Ctrl+C to detach (run continues), Ctrl+C twice to stop")
	}
	fmt.Println()

	ctx := context.Background()

	if interactive {
		return RunInteractive(ctx, manager, r, r.ExecCmd, attachTTYTrace)
	}

	return attachOutputMode(ctx, manager, r)
}

// attachOutputMode attaches in output-only mode (no stdin).
// Uses container logs with follow mode for reliable output streaming.
func attachOutputMode(ctx context.Context, manager *run.Manager, r *run.Run) error {
	// Set up signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	var lastSigTime time.Time

	// Create cancellable context for logs
	logsCtx, logsCancel := context.WithCancel(ctx)
	defer logsCancel()

	// Use logs with follow mode for output-only attach
	// This is more reliable than attach for containers already running
	logsDone := make(chan error, 1)
	go func() {
		logsDone <- manager.FollowLogs(logsCtx, r.ID, os.Stdout)
	}()

	// Also monitor if container exits
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- manager.Wait(ctx, r.ID)
	}()

	for {
		select {
		case sig := <-sigCh:
			now := time.Now()
			if now.Sub(lastSigTime) < doublePressWindow {
				// Double Ctrl+C - stop the run
				fmt.Printf("\nStopping run %s...\n", r.ID)
				logsCancel()
				if err := manager.Stop(context.Background(), r.ID); err != nil {
					log.Error("failed to stop run", "id", r.ID, "error", err)
				}
				fmt.Printf("Run %s stopped\n", r.ID)
				return nil
			}

			// First Ctrl+C - detach
			log.Debug("received signal, detaching", "signal", sig)
			fmt.Printf("\nDetaching from run %s (still running)\n", r.ID)
			fmt.Printf("Press Ctrl+C again within 500ms to stop\n")
			return nil

		case <-logsDone:
			// Logs ended - wait a moment for container exit to be detected
			select {
			case err := <-waitDone:
				if err != nil {
					return err
				}
				fmt.Printf("Run %s completed\n", r.ID)
				return nil
			case <-time.After(containerExitCheckDelay):
				// Container didn't exit quickly - check run state
				currentRun, getErr := manager.Get(r.ID)
				if getErr != nil || currentRun.State != run.StateRunning {
					// Run ended or was cleaned up
					fmt.Printf("Run %s completed\n", r.ID)
					return nil
				}
				// Container still running, logs stream ended
				fmt.Printf("\nDetached from run %s\n", r.ID)
				return nil
			}

		case err := <-waitDone:
			logsCancel()
			if err != nil {
				return err
			}
			fmt.Printf("Run %s completed\n", r.ID)
			return nil
		}
	}
}
