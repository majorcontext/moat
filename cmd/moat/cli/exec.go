package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/andybons/moat/internal/config"
	"github.com/andybons/moat/internal/log"
	"github.com/andybons/moat/internal/run"
	"github.com/andybons/moat/internal/term"
	"github.com/spf13/cobra"
)

// ExecFlags holds the common flags for container execution commands.
// These are shared between `moat run`, `moat claude`, and future tool commands.
type ExecFlags struct {
	Grants        []string
	Env           []string
	Name          string
	Rebuild       bool
	KeepContainer bool
	Detach        bool
	Interactive   bool
}

// AddExecFlags adds the common execution flags to a command.
func AddExecFlags(cmd *cobra.Command, flags *ExecFlags) {
	cmd.Flags().StringSliceVarP(&flags.Grants, "grant", "g", nil, "capabilities to grant (e.g., github, aws:s3.read)")
	cmd.Flags().StringArrayVarP(&flags.Env, "env", "e", nil, "environment variables (KEY=VALUE)")
	cmd.Flags().StringVarP(&flags.Name, "name", "n", "", "name for this run (default: from agent.yaml or random)")
	cmd.Flags().BoolVar(&flags.Rebuild, "rebuild", false, "force rebuild of container image (Docker only)")
	cmd.Flags().BoolVar(&flags.KeepContainer, "keep", false, "keep container after run completes (for debugging)")
	cmd.Flags().BoolVarP(&flags.Detach, "detach", "d", false, "run in background and return immediately")
}

// ExecOptions contains all the options needed to execute a containerized command.
type ExecOptions struct {
	// From flags
	Flags ExecFlags

	// Command-specific
	Workspace   string
	Command     []string
	Config      *config.Config
	Interactive bool // Can be set by flags or command logic
	TTY         bool

	// Callbacks for command-specific behavior
	OnRunCreated func(r *run.Run) // Called after run is created, before start
}

// ExecuteRun runs a containerized command with the given options.
// It handles creating the run, starting it, and managing the lifecycle.
// Returns the run for further inspection if needed.
func ExecuteRun(ctx context.Context, opts ExecOptions) (*run.Run, error) {
	// Create manager
	manager, err := run.NewManager()
	if err != nil {
		return nil, fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	// Build run options
	runOpts := run.Options{
		Name:          opts.Flags.Name,
		Workspace:     opts.Workspace,
		Grants:        opts.Flags.Grants,
		Cmd:           opts.Command,
		Config:        opts.Config,
		Env:           opts.Flags.Env,
		Rebuild:       opts.Flags.Rebuild,
		KeepContainer: opts.Flags.KeepContainer,
		Interactive:   opts.Interactive,
		TTY:           opts.TTY,
	}

	// Create run
	r, err := manager.Create(ctx, runOpts)
	if err != nil {
		return nil, fmt.Errorf("creating run: %w", err)
	}

	log.Info("created run", "id", r.ID, "name", r.Name)

	// Call the OnRunCreated callback if provided
	if opts.OnRunCreated != nil {
		opts.OnRunCreated(r)
	}

	// Start run
	startOpts := run.StartOptions{StreamLogs: !opts.Interactive}
	if err := manager.Start(ctx, r.ID, startOpts); err != nil {
		log.Error("failed to start run", "id", r.ID, "error", err)
		return r, fmt.Errorf("starting run: %w", err)
	}

	log.Info("run started", "id", r.ID)

	// Print port information if available
	if len(r.Ports) > 0 {
		globalCfg, _ := config.LoadGlobal()
		proxyPort := globalCfg.Proxy.Port

		fmt.Println("Services:")
		for serviceName, containerPort := range r.Ports {
			url := fmt.Sprintf("https://%s.%s.localhost:%d", serviceName, r.Name, proxyPort)
			fmt.Printf("  %s: %s (container :%d)\n", serviceName, url, containerPort)
		}
	}

	// If detached, return immediately
	if opts.Flags.Detach {
		fmt.Printf("\nRun %s started in background\n", r.ID)
		fmt.Printf("Use 'moat logs %s' to view output\n", r.ID)
		fmt.Printf("Use 'moat attach %s' to attach\n", r.ID)
		fmt.Printf("Use 'moat stop %s' to stop\n", r.ID)
		return r, nil
	}

	// Interactive mode: attach with stdin
	if opts.Interactive {
		return r, RunInteractive(ctx, manager, r)
	}

	// Non-interactive attached mode: stream output, Ctrl+C detaches
	return r, RunAttached(ctx, manager, r)
}

// RunAttached runs in attached mode where output is streamed but stdin is not connected.
// Ctrl+C detaches (leaves run active), double Ctrl+C stops the run.
func RunAttached(ctx context.Context, manager *run.Manager, r *run.Run) error {
	// Set up signal handling for detach behavior
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	// Track Ctrl+C timing for double-press detection
	lastSigTime := time.Time{}

	// Create a cancellable context for the wait
	waitCtx, waitCancel := context.WithCancel(ctx)
	defer waitCancel()

	// Channel to receive wait result
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- manager.Wait(waitCtx, r.ID)
	}()

	for {
		select {
		case sig := <-sigCh:
			now := time.Now()
			if now.Sub(lastSigTime) < doublePressWindow {
				// Double Ctrl+C - stop the run
				fmt.Printf("\nStopping run %s...\n", r.ID)
				waitCancel()
				if err := manager.Stop(context.Background(), r.ID); err != nil {
					log.Error("failed to stop run", "id", r.ID, "error", err)
				}
				fmt.Printf("Run %s stopped\n", r.ID)
				return nil
			}
			// First Ctrl+C - detach (double-press detection happens before this point)
			log.Debug("received signal, detaching", "signal", sig)
			fmt.Printf("\nDetaching from run %s (still running)\n", r.ID)
			fmt.Printf("Press Ctrl+C again within 500ms to stop, or use 'moat stop %s'\n", r.ID)
			fmt.Printf("Use 'moat attach %s' to reattach\n", r.ID)
			// Don't cancel - let the run continue
			return nil

		case err := <-waitDone:
			if err != nil {
				log.Error("run failed", "id", r.ID, "error", err)
				return err
			}
			log.Info("run completed", "id", r.ID)
			fmt.Printf("Run %s completed\n", r.ID)
			return nil
		}
	}
}

// RunInteractive runs in interactive mode with stdin connected and TTY allocated.
func RunInteractive(ctx context.Context, manager *run.Manager, r *run.Run) error {
	fmt.Printf("%s\n\n", term.EscapeHelpText())

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

	// Attach to container with escape-proxied stdin
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

	// Also wait for container to exit
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- manager.Wait(ctx, r.ID)
	}()

	for {
		select {
		case sig := <-sigCh:
			// In interactive mode, forward SIGINT to container (it will handle it)
			// Only SIGTERM causes us to stop
			if sig == syscall.SIGTERM {
				fmt.Printf("\nStopping run %s...\n", r.ID)
				attachCancel()
				if err := manager.Stop(context.Background(), r.ID); err != nil {
					log.Error("failed to stop run", "id", r.ID, "error", err)
				}
				return nil
			}
			// SIGINT is forwarded to container via attached stdin/tty

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
			// Container exited
			attachCancel() // Stop the attach goroutine
			if err != nil {
				log.Error("run failed", "id", r.ID, "error", err)
				return err
			}
			fmt.Printf("Run %s completed\n", r.ID)
			return nil
		}
	}
}
