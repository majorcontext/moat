package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/andybons/moat/internal/config"
	"github.com/andybons/moat/internal/log"
	"github.com/andybons/moat/internal/run"
	"github.com/andybons/moat/internal/term"
	"github.com/spf13/cobra"
)

// Timing constants for run behavior
const (
	// runDoublePressWindow is how quickly Ctrl+C must be pressed twice to stop
	runDoublePressWindow = 500 * time.Millisecond
	// runContainerExitCheckDelay is how long to wait for container exit detection
	runContainerExitCheckDelay = 200 * time.Millisecond
)

var (
	grants        []string
	runEnv        []string
	nameFlag      string
	rebuildFlag   bool
	keepContainer bool
	detachFlag    bool
	interactFlag  bool
	ttyFlag       bool
)

var runCmd = &cobra.Command{
	Use:   "run [path] [-- command]",
	Short: "Run an agent in an isolated environment",
	Long: `Run an agent in an isolated container with workspace mounting,
credential injection, and full observability.

The agent runs in a Docker container with your workspace mounted at /workspace.
If an agent.yaml exists in the workspace, its settings are used as defaults.

Arguments:
  [path]       Path to workspace directory (default: current directory)
  [-- cmd]     Optional command to run instead of agent's default

Non-interactive mode (default):
  Ctrl+C            Detach (run continues)
  Ctrl+C Ctrl+C     Stop the run (within 500ms)

Interactive mode (-it):
  Ctrl-/ d          Detach (run continues)
  Ctrl-/ k          Stop the run
  Ctrl+C            Sent to container process

Examples:
  # Run from current directory (uses agent.yaml config)
  moat run

  # Run from a specific directory
  moat run ./my-project

  # Run with a specific name for hostname routing
  moat run --name myapp ./my-project

  # Run with GitHub credentials
  moat run --grant github

  # Run with environment variables
  moat run -e DEBUG=true -e API_KEY=xxx

  # Run with custom command
  moat run -- npm test

  # Run multiple commands
  moat run -- sh -c "npm install && npm test"

  # Run detached (in background)
  moat run -d ./my-project

  # Run interactive shell
  moat run -it -- bash`,
	Args: cobra.ArbitraryArgs,
	RunE: runAgent,
}

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().StringSliceVar(&grants, "grant", nil, "capabilities to grant (e.g., github, aws:s3.read)")
	runCmd.Flags().StringArrayVarP(&runEnv, "env", "e", nil, "environment variables (KEY=VALUE)")
	runCmd.Flags().StringVar(&nameFlag, "name", "", "name for this agent instance (default: from agent.yaml or random)")
	runCmd.Flags().BoolVar(&rebuildFlag, "rebuild", false, "force rebuild of container image (Docker only, ignored for Apple containers)")
	runCmd.Flags().BoolVar(&keepContainer, "keep", false, "keep container after run completes (for debugging)")
	runCmd.Flags().BoolVarP(&detachFlag, "detach", "d", false, "run in background and return immediately")
	runCmd.Flags().BoolVarP(&interactFlag, "interactive", "i", false, "keep stdin open for interactive input")
	runCmd.Flags().BoolVarP(&ttyFlag, "tty", "t", false, "allocate a pseudo-TTY")
}

func runAgent(cmd *cobra.Command, args []string) error {
	// Parse args: [path] [-- command...]
	workspacePath := "."
	var containerCmd []string

	// Check if there's a -- separator
	dashIdx := cmd.ArgsLenAtDash()
	if dashIdx >= 0 {
		// Args before -- are path (if any)
		if dashIdx > 0 {
			workspacePath = args[0]
		}
		// Args after -- are the command
		containerCmd = args[dashIdx:]
	} else {
		// No --, so first arg (if present) is path
		if len(args) > 0 {
			workspacePath = args[0]
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

	// Load agent.yaml if present
	cfg, err := config.Load(absPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Determine agent name: --name flag > config.Name > random
	agentInstanceName := nameFlag
	if agentInstanceName == "" && cfg != nil && cfg.Name != "" {
		agentInstanceName = cfg.Name
	}
	// Random name generation happens in manager.Create if still empty

	// Apply config defaults
	if cfg != nil {
		if len(grants) == 0 && len(cfg.Grants) > 0 {
			grants = cfg.Grants
		}
		if len(containerCmd) == 0 && len(cfg.Command) > 0 {
			containerCmd = cfg.Command
		}
	}

	// Determine interactive mode: CLI flags > config > default
	interactive := interactFlag || ttyFlag
	if !interactive && cfg != nil && cfg.Interactive {
		interactive = true
	}

	// Warn if command looks interactive but -it wasn't specified
	if !interactive && len(containerCmd) > 0 {
		cmdName := containerCmd[0]
		// Check for common interactive commands
		if cmdName == "bash" || cmdName == "sh" || cmdName == "zsh" ||
			cmdName == "/bin/bash" || cmdName == "/bin/sh" || cmdName == "/bin/zsh" {
			// Only warn if no additional args (bare shell invocation)
			if len(containerCmd) == 1 {
				fmt.Fprintf(os.Stderr, "Hint: '%s' is interactive. Consider using: moat run -it -- %s\n\n", cmdName, cmdName)
			}
		}
	}

	log.Debug("preparing run",
		"name", agentInstanceName,
		"workspace", absPath,
		"grants", grants,
		"cmd", containerCmd,
		"detach", detachFlag,
		"interactive", interactive,
	)

	if dryRun {
		fmt.Println("Dry run - would start agent container")
		if len(containerCmd) > 0 {
			fmt.Printf("Command: %v\n", containerCmd)
		}
		return nil
	}

	// Create manager
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	// Set up base context (no signal handling yet - we handle signals specially)
	ctx := context.Background()

	opts := run.Options{
		Name:          agentInstanceName,
		Workspace:     absPath,
		Grants:        grants,
		Cmd:           containerCmd,
		Config:        cfg,
		Env:           runEnv,
		Rebuild:       rebuildFlag,
		KeepContainer: keepContainer,
		Interactive:   interactive,
		TTY:           ttyFlag || interactFlag, // -i implies -t for convenience
	}

	// Create run
	r, err := manager.Create(ctx, opts)
	if err != nil {
		return fmt.Errorf("creating run: %w", err)
	}

	log.Info("created run", "id", r.ID, "name", r.Name)

	// Start run
	// Don't stream logs for interactive mode - attach handles I/O
	startOpts := run.StartOptions{StreamLogs: !interactive}
	if err := manager.Start(ctx, r.ID, startOpts); err != nil {
		log.Error("failed to start run", "id", r.ID, "error", err)
		return fmt.Errorf("starting run: %w", err)
	}

	log.Info("run started", "id", r.ID)
	fmt.Printf("Started agent %q (run %s)\n", r.Name, r.ID)

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
	if detachFlag {
		fmt.Printf("\nRun %s started in background\n", r.ID)
		fmt.Printf("Use 'moat logs %s' to view output\n", r.ID)
		fmt.Printf("Use 'moat attach %s' to attach\n", r.ID)
		fmt.Printf("Use 'moat stop %s' to stop\n", r.ID)
		return nil
	}

	fmt.Println()

	// Interactive mode: attach with stdin
	if interactive {
		return runInteractive(ctx, manager, r)
	}

	// Non-interactive attached mode: stream output, Ctrl+C detaches
	return runAttached(ctx, manager, r)
}

// runAttached runs in attached mode where output is streamed but stdin is not connected.
// Ctrl+C detaches (leaves run active), double Ctrl+C stops the run.
func runAttached(ctx context.Context, manager *run.Manager, r *run.Run) error {
	// Set up signal handling for detach behavior
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	// Track Ctrl+C timing for double-press detection
	var lastSigTime time.Time

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
			if now.Sub(lastSigTime) < runDoublePressWindow {
				// Double Ctrl+C - stop the run
				fmt.Printf("\nStopping run %s...\n", r.ID)
				waitCancel()
				if err := manager.Stop(context.Background(), r.ID); err != nil {
					log.Error("failed to stop run", "id", r.ID, "error", err)
				}
				fmt.Printf("Run %s stopped\n", r.ID)
				return nil
			}

			// First Ctrl+C - detach
			lastSigTime = now
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

// runInteractive runs in interactive mode with stdin connected and TTY allocated.
func runInteractive(ctx context.Context, manager *run.Manager, r *run.Run) error {
	fmt.Printf("%s\n\n", term.EscapeHelpText())

	// Put terminal in raw mode to capture escape sequences without echo
	if term.IsTerminal(os.Stdin) {
		rawState, err := term.EnableRawMode(os.Stdin)
		if err != nil {
			log.Debug("failed to enable raw mode", "error", err)
			// Continue without raw mode - escapes may echo
		} else {
			defer term.RestoreTerminal(rawState)
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
			case <-time.After(runContainerExitCheckDelay):
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
