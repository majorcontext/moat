package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	intcli "github.com/majorcontext/moat/internal/cli"
	clipboardpkg "github.com/majorcontext/moat/internal/clipboard"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/snapshot"
	"github.com/majorcontext/moat/internal/term"
	"github.com/majorcontext/moat/internal/trace"
	"github.com/majorcontext/moat/internal/tui"
	"github.com/majorcontext/moat/internal/ui"
	"github.com/spf13/cobra"
)

// Timing constants for interactive execution behavior
const (
	// ttyStartupDelay is how long to wait before resizing TTY after container starts.
	// This allows the container process to initialize before we resize.
	ttyStartupDelay = 200 * time.Millisecond
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
	intcli.CheckWorktreeActive = checkWorktreeActive
}

// checkWorktreeActive checks if there is a running run in the given worktree path.
func checkWorktreeActive(worktreePath string) (string, string) {
	manager, err := run.NewManager()
	if err != nil {
		return "", ""
	}
	defer manager.Close()

	for _, r := range manager.List() {
		if r.WorktreePath == worktreePath && r.GetState() == run.StateRunning {
			return r.Name, r.ID
		}
	}
	return "", ""
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
	if r.DaemonCommit != "" && r.DaemonCommit != commit {
		bar.SetWarning("proxy stale")
	}
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

	// Set runtime based on CLI flag or moat.yaml, in priority order:
	// 1. --runtime CLI flag (if provided)
	// 2. moat.yaml runtime field (if set)
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

	// Resolve clipboard mode: --no-clipboard flag > config > default (true)
	clipboard := opts.Interactive && !opts.Flags.NoClipboard
	if clipboard && opts.Config != nil && opts.Config.Clipboard != nil && !*opts.Config.Clipboard {
		clipboard = false
	}

	// Append CLI --mount flags to config mounts
	for _, ms := range opts.Flags.Mounts {
		me, parseErr := config.ParseMount(ms)
		if parseErr != nil {
			return nil, fmt.Errorf("parsing --mount flag: %w", parseErr)
		}
		if opts.Config == nil {
			opts.Config = &config.Config{}
		}
		for _, existing := range opts.Config.Mounts {
			if existing.Target == me.Target {
				return nil, fmt.Errorf("--mount %s: target %q already mounted", ms, me.Target)
			}
		}
		opts.Config.Mounts = append(opts.Config.Mounts, *me)
	}

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
		Clipboard:     clipboard,
	}

	// Create run
	r, err := manager.Create(ctx, runOpts)
	if err != nil {
		return nil, fmt.Errorf("creating run: %w", err)
	}

	log.Info("created run", "id", r.ID, "name", r.Name)

	// Set worktree metadata if this run was created via moat wt or --wt
	if opts.WorktreeBranch != "" {
		r.WorktreeBranch = opts.WorktreeBranch
		r.WorktreePath = opts.WorktreePath
		r.WorktreeRepoID = opts.WorktreeRepoID
		if err := r.SaveMetadata(); err != nil {
			log.Warn("failed to save worktree metadata", "error", err)
		}
	}

	// Call the OnRunCreated callback if provided (provider commands set this).
	// For moat run, print the run info here so it appears before the session starts.
	if opts.OnRunCreated != nil {
		opts.OnRunCreated(intcli.RunInfo{
			ID:   r.ID,
			Name: r.Name,
		})
	} else {
		fmt.Printf("Started %s (%s)\n", r.Name, r.ID)
	}

	// Interactive mode: use StartAttached to ensure TTY is connected before process starts.
	// This is required for TUI applications like Codex CLI that need to detect terminal
	// capabilities immediately on startup.
	if opts.Interactive {
		return r, RunInteractiveAttached(ctx, manager, r, opts.Command, opts.Flags.TTYTrace)
	}

	// Non-interactive: start the container, stream its output, and wait for exit.
	if err := manager.Start(ctx, r.ID, run.StartOptions{}); err != nil {
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

	fmt.Println(ui.Dim("Press Ctrl+C to stop"))
	fmt.Println()

	// Stream container logs to stdout in a goroutine.
	// FollowLogs blocks until the container exits or context is canceled.
	logCtx, logCancel := context.WithCancel(ctx)
	defer logCancel()
	go func() {
		if err := manager.FollowLogs(logCtx, r.ID, os.Stdout); err != nil && logCtx.Err() == nil {
			log.Debug("log streaming ended", "error", err)
		}
	}()

	// Wait for container exit or signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- manager.Wait(ctx, r.ID)
	}()

	select {
	case sig := <-sigCh:
		log.Info("received signal, stopping run", "signal", sig, "id", r.ID)
		logCancel()
		fmt.Printf("\nStopping run %s...\n", r.ID)
		if err := manager.Stop(ctx, r.ID); err != nil {
			log.Error("failed to stop run", "id", r.ID, "error", err)
		}
		// Wait for monitorContainerExit to finish cleanup
		<-waitDone
		fmt.Println()
		fmt.Println(ui.Dim(fmt.Sprintf("View output: moat logs %s", r.ID)))
		return r, nil
	case err := <-waitDone:
		logCancel()
		if err != nil {
			return r, fmt.Errorf("run failed: %w", err)
		}
		fmt.Println()
		fmt.Println(ui.Dim(fmt.Sprintf("View output: moat logs %s", r.ID)))
		return r, nil
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

	// Wrap stdin with escape proxy to detect stop sequences
	escapeProxy := term.NewEscapeProxy(os.Stdin)

	// Set up callback to update footer when escape sequence is in progress
	if statusWriter != nil {
		statusWriter.SetupEscapeHints(escapeProxy)
	}

	// Set up callback for non-disruptive escape actions (snapshot)
	var flashMu sync.Mutex
	var flashTimer *time.Timer
	escapeProxy.OnAction(func(action term.EscapeAction) {
		if action == term.EscapeSnapshot {
			go takeSnapshot(r, statusWriter, &flashMu, &flashTimer)
		}
	})

	// Build stdin reader chain: escape proxy -> clipboard proxy (optional) -> tracer (optional)
	var stdin io.Reader = escapeProxy
	if r.Clipboard {
		stdin = term.NewClipboardProxy(stdin, func() {
			done := make(chan struct{})
			go func() {
				defer close(done)
				clipCtx, clipCancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer clipCancel()
				content, err := clipboardpkg.Read()
				if err != nil || content == nil {
					return
				}
				target := clipboardpkg.MIMEToXclipTarget(content.MIMEType)
				_ = manager.WriteClipboard(clipCtx, r.ID, content.Data, target)
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
			}
		})
	}
	if tracer != nil {
		stdin = trace.NewRecordingReader(stdin, tracer.recorder, trace.EventStdin)
	}

	// Set up signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	// Create cancellable context for the attach
	attachCtx, attachCancel := context.WithCancel(ctx)
	defer attachCancel()

	// Start with attachment - this ensures TTY is connected before process starts
	attachDone := make(chan error, 1)
	go func() {
		attachDone <- manager.StartAttached(attachCtx, r.ID, stdin, stdout, os.Stderr)
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

		case err := <-attachDone:
			if term.GetEscapeAction(err) == term.EscapeStop {
				fmt.Printf("\r\nStopping run %s...\r\n", r.ID)
				if stopErr := manager.Stop(context.Background(), r.ID); stopErr != nil {
					log.Error("failed to stop run", "id", r.ID, "error", stopErr)
				}
				fmt.Printf("Run %s stopped\r\n", r.ID)
				return nil
			}
			if err != nil && ctx.Err() == nil {
				log.Error("run failed", "id", r.ID, "error", err)
				return fmt.Errorf("run failed: %w", err)
			}
			fmt.Printf("Run %s completed\n", r.ID)
			return nil
		}
	}
}

// takeSnapshot creates a manual snapshot and shows the result in the status bar.
// The flashMu/flashTimer pair are shared across calls to prevent rapid snapshots
// from clearing each other's flash message.
func takeSnapshot(r *run.Run, statusWriter *tui.Writer, flashMu *sync.Mutex, flashTimer **time.Timer) {
	flash := func(msg string) {
		if statusWriter == nil {
			return
		}
		flashMu.Lock()
		defer flashMu.Unlock()
		if *flashTimer != nil {
			(*flashTimer).Stop()
		}
		statusWriter.SetMessage(msg)
		_ = statusWriter.UpdateStatus()
		*flashTimer = time.AfterFunc(2*time.Second, func() {
			statusWriter.ClearMessage()
			_ = statusWriter.UpdateStatus()
		})
	}

	if r.SnapEngine == nil {
		flash("Snapshots not configured")
		return
	}

	snap, err := r.SnapEngine.Create(snapshot.TypeManual, "")
	if err != nil {
		log.Error("manual snapshot failed", "error", err)
		flash("Snapshot failed: " + err.Error())
		return
	}

	flash("Snapshot saved: " + snap.ID)
}
