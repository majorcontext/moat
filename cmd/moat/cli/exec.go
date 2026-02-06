package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	intcli "github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/term"
	"github.com/majorcontext/moat/internal/trace"
	"github.com/majorcontext/moat/internal/tui"
	"github.com/majorcontext/moat/internal/ui"
	"github.com/spf13/cobra"
)

// Re-export types from internal/cli for backward compatibility
// with code in cmd/moat/cli that uses these types.
type ExecFlags = intcli.ExecFlags
type ExecOptions = intcli.ExecOptions

// AddExecFlags adds the common execution flags to a command.
func AddExecFlags(cmd *cobra.Command, flags *ExecFlags) {
	intcli.AddExecFlags(cmd, flags)
}

func init() {
	// Register the ExecuteRun function in the internal/cli globals
	// so that provider packages can use it without import cycles.
	intcli.ExecuteRun = executeRunWrapper
}

// executeRunWrapper wraps ExecuteRun to match the function signature in intcli.
func executeRunWrapper(ctx context.Context, opts intcli.ExecOptions) (*intcli.ExecResult, error) {
	r, err := ExecuteRun(ctx, opts)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, nil
	}
	return &intcli.ExecResult{
		ID:   r.ID,
		Name: r.Name,
	}, nil
}

// setupStatusBar creates a status bar for interactive container sessions.
// Returns the writer (which wraps stdout with status bar compositing), a cleanup
// function that must be deferred, and the output writer to use for container output.
// If stdout is not a TTY or setup fails, returns nil writer with os.Stdout as output.
func setupStatusBar(manager *run.Manager, r *run.Run) (writer *tui.Writer, cleanup func(), stdout io.Writer) {
	stdout = os.Stdout
	cleanup = func() {} // no-op by default

	if !term.IsTerminal(os.Stdout) {
		return nil, cleanup, stdout
	}

	width, height := term.GetSize(os.Stdout)
	if width <= 0 || height <= 0 {
		return nil, cleanup, stdout
	}

	runtimeType := manager.RuntimeType()
	bar := tui.NewStatusBar(r.ID, r.Name, runtimeType)
	bar.SetGrants(r.Grants)
	bar.SetDimensions(width, height)
	writer = tui.NewWriter(os.Stdout, bar, runtimeType)

	if err := writer.Setup(); err != nil {
		log.Debug("failed to setup status bar", "error", err)
		return nil, cleanup, os.Stdout
	}

	// Sync stdout to ensure terminal has processed setup before container starts
	_ = os.Stdout.Sync()

	cleanup = func() {
		if err := writer.Cleanup(); err != nil {
			log.Debug("failed to cleanup status bar", "error", err)
		}
	}

	return writer, cleanup, writer
}

// ttyTracer holds the state for TTY tracing during an interactive session.
type ttyTracer struct {
	recorder *trace.Recorder
	path     string
}

// setupTTYTracer creates a TTY tracer if trace path is specified.
// Returns nil if tracing is disabled or setup fails.
func setupTTYTracer(tracePath string, r *run.Run, command []string) *ttyTracer {
	if tracePath == "" {
		return nil
	}

	// Get initial terminal size
	width, height := 80, 24 // defaults
	if term.IsTerminal(os.Stdout) {
		w, h := term.GetSize(os.Stdout)
		if w > 0 && h > 0 {
			width, height = w, h
		}
	}

	// Create recorder
	recorder := trace.NewRecorder(
		r.ID,
		command,
		trace.GetTraceEnv(),
		trace.Size{Width: width, Height: height},
	)

	log.Info("TTY tracing enabled", "path", tracePath, "run_id", r.ID)
	fmt.Printf("Recording terminal I/O to %s\n", tracePath)

	return &ttyTracer{
		recorder: recorder,
		path:     tracePath,
	}
}

// save saves the trace to disk.
func (t *ttyTracer) save() {
	if t == nil || t.recorder == nil {
		return
	}

	if err := t.recorder.Save(t.path); err != nil {
		log.Error("failed to save TTY trace", "path", t.path, "error", err)
		ui.Warnf("Failed to save terminal trace to %s: %v", t.path, err)
	} else {
		log.Info("TTY trace saved", "path", t.path)
		fmt.Printf("Terminal trace saved to %s\n", t.path)
	}
}

// ExecuteRun runs a containerized command with the given options.
// It handles creating the run, starting it, and managing the lifecycle.
// Returns the run for further inspection if needed.
func ExecuteRun(ctx context.Context, opts intcli.ExecOptions) (*run.Run, error) {
	fmt.Println("Initializing...")

	// Set runtime based on CLI flag or agent.yaml, in priority order:
	// 1. --runtime CLI flag (if provided)
	// 2. agent.yaml runtime field (if set)
	// Both override the MOAT_RUNTIME env var and auto-detection (handled in detect.go)
	if opts.Flags.Runtime != "" {
		os.Setenv("MOAT_RUNTIME", opts.Flags.Runtime)
	} else if opts.Config != nil && opts.Config.Runtime != "" {
		os.Setenv("MOAT_RUNTIME", opts.Config.Runtime)
	}
	// If neither is set, detect.go checks MOAT_RUNTIME env var, then auto-detects

	// Create manager
	var managerOpts run.ManagerOptions
	if opts.Flags.NoSandbox {
		noSandbox := true
		managerOpts.NoSandbox = &noSandbox
	}
	manager, err := run.NewManagerWithOptions(managerOpts)
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
		opts.OnRunCreated(intcli.RunInfo{
			ID:   r.ID,
			Name: r.Name,
		})
	}

	// Interactive mode: use StartAttached to ensure TTY is connected before process starts
	// This is required for TUI applications like Codex CLI that need to detect terminal
	// capabilities immediately on startup.
	if opts.Interactive && !opts.Flags.Detach {
		return r, RunInteractiveAttached(ctx, manager, r, opts.Command, opts.Flags.TTYTrace)
	}

	// Start run (non-interactive or detached)
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

		fmt.Println("Endpoints:")
		for endpointName, containerPort := range r.Ports {
			url := fmt.Sprintf("https://%s.%s.localhost:%d", endpointName, r.Name, proxyPort)
			fmt.Printf("  %s: %s (container :%d)\n", endpointName, url, containerPort)
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

	// Resize container TTY to match terminal size
	if term.IsTerminal(os.Stdout) {
		width, height := term.GetSize(os.Stdout)
		if width > 0 && height > 0 {
			// #nosec G115 -- width/height are validated positive above
			if err := manager.ResizeTTY(ctx, r.ID, uint(height), uint(width)); err != nil {
				log.Debug("failed to resize TTY", "error", err)
			}
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
				// Wait for attach goroutine to complete before checking state
				select {
				case <-attachDone:
				case <-time.After(500 * time.Millisecond):
				}
				currentRun, _ := manager.Get(r.ID)
				if currentRun != nil && currentRun.State == run.StateRunning {
					fmt.Printf("\r\nDetached from run %s (still running)\r\n", r.ID)
					fmt.Printf("Use 'moat attach %s' to reattach\r\n", r.ID)
				} else {
					fmt.Printf("\r\nRun %s stopped\r\n", r.ID)
				}
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

// RunInteractiveAttached runs in interactive mode using StartAttached to ensure
// the TTY is connected before the container process starts. This is required for
// TUI applications (like Codex CLI) that need to detect terminal capabilities
// immediately on startup (e.g., reading cursor position).
func RunInteractiveAttached(ctx context.Context, manager *run.Manager, r *run.Run, command []string, tracePath string) error {
	fmt.Printf("%s\n\n", term.EscapeHelpText())

	// Set up TTY tracing if requested
	tracer := setupTTYTracer(tracePath, r, command)
	defer tracer.save()

	// Put terminal in raw mode to capture escape sequences without echo
	var rawState *term.RawModeState
	if term.IsTerminal(os.Stdin) {
		var err error
		rawState, err = term.EnableRawMode(os.Stdin)
		if err != nil {
			log.Debug("failed to enable raw mode", "error", err)
			// Continue without raw mode - escapes may echo
		}
	}

	// Ensure terminal is restored on exit
	defer func() {
		if rawState != nil {
			if err := term.RestoreTerminal(rawState); err != nil {
				log.Debug("failed to restore terminal", "error", err)
			}
		}
	}()

	// Set up status bar for interactive session
	statusWriter, statusCleanup, stdout := setupStatusBar(manager, r)
	defer statusCleanup()

	// Wrap stdout with tracer if tracing is enabled
	if tracer != nil {
		stdout = trace.NewRecordingWriter(stdout, tracer.recorder, trace.EventStdout)
	}

	// Wrap stdin with escape proxy to detect detach/stop sequences
	escapeProxy := term.NewEscapeProxy(os.Stdin)

	// Set up callback to update footer when escape sequence is in progress
	if statusWriter != nil {
		statusWriter.SetupEscapeHints(escapeProxy)
	}

	// Wrap stdin with tracer if tracing is enabled
	stdin := io.Reader(escapeProxy)
	if tracer != nil {
		stdin = trace.NewRecordingReader(escapeProxy, tracer.recorder, trace.EventStdin)
	}

	// Set up signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	// Channel to receive escape actions from the attach goroutine
	escapeCh := make(chan term.EscapeAction, 1)

	// Create cancellable context for the attach
	attachCtx, attachCancel := context.WithCancel(ctx)
	defer attachCancel()

	// Start with attachment - this ensures TTY is connected before process starts
	attachDone := make(chan error, 1)
	go func() {
		err := manager.StartAttached(attachCtx, r.ID, stdin, stdout, os.Stderr)
		// Check if the error is an escape sequence
		if term.IsEscapeError(err) {
			escapeCh <- term.GetEscapeAction(err)
			attachDone <- nil
		} else {
			attachDone <- err
		}
	}()

	// Give container a moment to start, then resize TTY to match terminal.
	// Note: We don't call statusWriter.Resize() here because Setup() already
	// configured the scroll region and status bar with the correct dimensions.
	// Calling Resize() again can interfere with the shell's cursor positioning
	// during initialization. The status bar will be resized on SIGWINCH events.
	go func() {
		time.Sleep(ttyStartupDelay)
		if term.IsTerminal(os.Stdout) {
			width, height := term.GetSize(os.Stdout)
			if width > 0 && height > 0 {
				// #nosec G115 -- width/height are validated positive above
				if err := manager.ResizeTTY(ctx, r.ID, uint(height), uint(width)); err != nil {
					log.Debug("failed to resize TTY", "error", err)
				}
			}
		}
	}()

	for {
		select {
		case sig := <-sigCh:
			if sig == syscall.SIGWINCH {
				// Handle terminal resize
				if statusWriter != nil && term.IsTerminal(os.Stdout) {
					width, height := term.GetSize(os.Stdout)
					if width > 0 && height > 0 {
						// Record resize event for tracing
						if tracer != nil {
							tracer.recorder.AddResize(width, height)
						}
						_ = statusWriter.Resize(width, height)
						// Also resize container TTY
						// #nosec G115 -- width/height are validated positive above
						_ = manager.ResizeTTY(ctx, r.ID, uint(height), uint(width))
					}
				}
				continue // Don't break out of loop
			}
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
				// Wait for attach goroutine to complete before checking state
				select {
				case <-attachDone:
				case <-time.After(500 * time.Millisecond):
				}
				currentRun, _ := manager.Get(r.ID)
				if currentRun != nil && currentRun.State == run.StateRunning {
					fmt.Printf("\r\nDetached from run %s (still running)\r\n", r.ID)
					fmt.Printf("Use 'moat attach %s' to reattach\r\n", r.ID)
				} else {
					fmt.Printf("\r\nRun %s stopped\r\n", r.ID)
				}
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
			// Attachment ended (container exited or error)
			if err != nil && ctx.Err() == nil && !term.IsEscapeError(err) {
				log.Error("run failed", "id", r.ID, "error", err)
				return fmt.Errorf("run failed: %w", err)
			}
			fmt.Printf("Run %s completed\n", r.ID)
			return nil
		}
	}
}
