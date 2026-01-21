package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/andybons/moat/internal/log"
	"github.com/andybons/moat/internal/run"
	"github.com/andybons/moat/internal/term"
	"github.com/spf13/cobra"
)

// Timing constants for attach behavior
const (
	// doublePressWindow is how quickly Ctrl+C must be pressed twice to stop a run
	doublePressWindow = 500 * time.Millisecond
	// containerExitCheckDelay is how long to wait for container exit detection
	containerExitCheckDelay = 200 * time.Millisecond
)

var attachInteractive bool

var attachCmd = &cobra.Command{
	Use:   "attach <run-id>",
	Short: "Attach to a running agent",
	Long: `Attach local stdin, stdout, and stderr to a running agent.

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
  # Attach to see output (or interactive if run was started with -i)
  moat attach run_abc12345

  # Force interactive mode
  moat attach -i run_abc12345

  # Force output-only mode even if run was started interactively
  moat attach -i=false run_abc12345`,
	Args: cobra.ExactArgs(1),
	RunE: attachToRun,
}

func init() {
	rootCmd.AddCommand(attachCmd)
	attachCmd.Flags().BoolVarP(&attachInteractive, "interactive", "i", false, "interactive mode (use -i=false to force output-only)")
}

func attachToRun(cmd *cobra.Command, args []string) error {
	runID := args[0]

	// Create manager
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	// Verify run exists and is running
	r, err := manager.Get(runID)
	if err != nil {
		return fmt.Errorf("run not found: %w", err)
	}

	if r.State != run.StateRunning {
		return fmt.Errorf("run %s is not running (state: %s)", runID, r.State)
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
		return attachInteractiveMode(ctx, manager, r)
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

// attachInteractiveMode attaches with stdin connected.
func attachInteractiveMode(ctx context.Context, manager *run.Manager, r *run.Run) error {
	// Show recent logs before attaching so user has context
	if logs, err := manager.RecentLogs(r.ID, 50); err == nil && len(logs) > 0 {
		fmt.Print(logs)
		// Add a newline if logs don't end with one
		if len(logs) > 0 && logs[len(logs)-1] != '\n' {
			fmt.Println()
		}
	}

	// Put terminal in raw mode to capture escape sequences without echo
	if term.IsTerminal(os.Stdin) {
		rawState, err := term.EnableRawMode(os.Stdin)
		if err != nil {
			log.Debug("failed to enable raw mode", "error", err)
			// Continue without raw mode - escapes may echo
		} else {
			defer func() {
				if err := term.RestoreTerminal(rawState); err != nil {
					log.Debug("failed to restore terminal", "error", err)
				}
			}()
		}
	}

	// Set up signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	// Wrap stdin with escape proxy to detect detach/stop sequences
	escapeProxy := term.NewEscapeProxy(os.Stdin)

	// Channel to receive escape actions from the attach goroutine
	escapeCh := make(chan term.EscapeAction, 1)

	attachCtx, attachCancel := context.WithCancel(ctx)
	defer attachCancel()

	attachDone := make(chan error, 1)
	go func() {
		err := manager.Attach(attachCtx, r.ID, escapeProxy, os.Stdout, os.Stderr)
		// Check if the error is an escape sequence
		if term.IsEscapeError(err) {
			escapeCh <- term.GetEscapeAction(err)
			attachDone <- nil
		} else {
			attachDone <- err
		}
	}()

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- manager.Wait(ctx, r.ID)
	}()

	for {
		select {
		case sig := <-sigCh:
			if sig == syscall.SIGTERM {
				fmt.Printf("\nStopping run %s...\n", r.ID)
				attachCancel()
				if err := manager.Stop(context.Background(), r.ID); err != nil {
					log.Error("failed to stop run", "id", r.ID, "error", err)
				}
				return nil
			}
			// SIGINT is forwarded to container via stdin/tty

		case action := <-escapeCh:
			// Handle escape sequence
			switch action {
			case term.EscapeDetach:
				attachCancel()
				fmt.Printf("\r\nDetached from run %s (still running)\r\n", r.ID)
				fmt.Printf("Use 'moat attach %s' to reattach\r\n", r.ID)
				return nil

			case term.EscapeStop:
				fmt.Printf("\r\nStopping run %s...\r\n", r.ID)
				attachCancel()
				if err := manager.Stop(context.Background(), r.ID); err != nil {
					log.Error("failed to stop run", "id", r.ID, "error", err)
				}
				fmt.Printf("Run %s stopped\r\n", r.ID)
				return nil
			}

		case err := <-attachDone:
			// Attach ended - wait a moment for container exit to be detected
			if err != nil && ctx.Err() == nil && !term.IsEscapeError(err) {
				log.Error("attach failed", "id", r.ID, "error", err)
			}
			// Give the wait goroutine time to detect container exit
			select {
			case waitErr := <-waitDone:
				if waitErr != nil {
					return waitErr
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
				// Container still running, we just got disconnected
				fmt.Printf("\nDetached from run %s\n", r.ID)
				return nil
			}

		case err := <-waitDone:
			attachCancel()
			if err != nil {
				return err
			}
			fmt.Printf("Run %s completed\n", r.ID)
			return nil
		}
	}
}
