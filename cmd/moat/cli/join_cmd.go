package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/term"
	"github.com/majorcontext/moat/internal/tui"
)

var (
	joinContinue bool
	joinResume   string
	joinPrompt   string
)

var joinCmd = &cobra.Command{
	Use:   "join <run> <agent> [flags]",
	Short: "Launch another agent inside a running container",
	Long: `Launch a second agent inside an already-running container, reusing its
workspace, grants, and credentials — without creating a new container.

The agent must match the one the run was started with (v1 supports same-agent
joins, e.g. joining claude into a run started by 'moat claude').

Examples:
  moat join run_a1b2c3d4e5f6 claude
  moat join my-feature claude --continue
  moat join run_a1b2c3d4e5f6 claude -p "summarize the diff"`,
	Args: cobra.MinimumNArgs(2),
	RunE: runJoin,
}

func init() {
	joinCmd.Flags().BoolVarP(&joinContinue, "continue", "c", false, "continue the most recent conversation")
	joinCmd.Flags().StringVarP(&joinResume, "resume", "r", "", "resume a specific session by ID")
	joinCmd.Flags().StringVarP(&joinPrompt, "prompt", "p", "", "run with prompt (non-interactive)")
	rootCmd.AddCommand(joinCmd)
}

// validateJoinAgent checks that the run (whose recorded agent field is runAgent)
// was created by the requested provider. agentArg is the user-typed agent name,
// used only for the error message.
func validateJoinAgent(j provider.JoinableAgent, agentArg, runAgent string) error {
	if !j.IdentifiesAs(runAgent) {
		return fmt.Errorf("run has no %s configuration.\n"+
			"v1 join only attaches an agent the run was started with (run agent: %q).\n"+
			"To run %s here, start the run with %s configured.",
			agentArg, runAgent, agentArg, agentArg)
	}
	return nil
}

// joinableAgentNames returns the sorted names of registered agents that support
// `moat join`, for use in the unknown-agent error message.
func joinableAgentNames() []string {
	var names []string
	for _, a := range provider.Agents() {
		if _, ok := a.(provider.JoinableAgent); ok {
			names = append(names, a.Name())
		}
	}
	sort.Strings(names)
	return names
}

func runJoin(cmd *cobra.Command, args []string) error {
	if joinContinue && joinResume != "" {
		return fmt.Errorf("--continue and --resume are mutually exclusive")
	}

	runArg := args[0]
	agentArg := args[1]

	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	runID, err := resolveRunArgSingle(manager, runArg)
	if err != nil {
		return err
	}

	r, gErr := manager.Get(runID)
	if gErr != nil {
		return gErr
	}
	if r.GetState() != run.StateRunning {
		return fmt.Errorf("run %s is not running (state: %s)", runID, r.GetState())
	}

	agent := provider.GetAgent(agentArg)
	if agent == nil {
		return fmt.Errorf("unknown agent %q; joinable agents: %s", agentArg, strings.Join(joinableAgentNames(), ", "))
	}
	joinable, ok := agent.(provider.JoinableAgent)
	if !ok {
		return fmt.Errorf("agent %q does not support join yet", agentArg)
	}
	if valErr := validateJoinAgent(joinable, agentArg, r.Agent); valErr != nil {
		return valErr
	}

	containerCmd, err := joinable.JoinCommand(provider.JoinOpts{
		Continue: joinContinue,
		Resume:   joinResume,
		Prompt:   joinPrompt,
	})
	if err != nil {
		return err
	}

	ctx := context.Background()

	// Register this join for the display-only attached count. Both interactive
	// and headless paths need an index so console output lands in logs.<N>.jsonl.
	index, release, regErr := manager.RegisterJoinedAgent(runID)
	if regErr == nil {
		defer release()
	}

	// Headless (--prompt with no TTY) vs interactive.
	if joinPrompt != "" || !term.IsTerminal(os.Stdin) || !term.IsTerminal(os.Stdout) {
		var headlessOut io.Writer = os.Stdout
		if regErr == nil && r.Store != nil {
			if lw, lerr := r.Store.JoinLogWriter(index); lerr == nil {
				defer lw.Close()
				headlessOut = io.MultiWriter(os.Stdout, lw)
			}
		}
		execErr := manager.ExecInteractive(ctx, runID, containerCmd, container.ExecOptions{
			Stdin:  nil,
			Stdout: headlessOut,
			Stderr: os.Stderr,
			TTY:    false,
		})
		return handleJoinExecErr(manager, execErr)
	}

	return runJoinInteractive(ctx, manager, r, containerCmd, index, regErr == nil)
}

func handleJoinExecErr(manager *run.Manager, execErr error) error {
	if execErr == nil {
		return nil
	}
	var ee *container.ExecError
	if errors.As(execErr, &ee) {
		manager.Close()
		os.Exit(ee.ExitCode)
	}
	return execErr
}

func setupJoinStatusBar(manager *run.Manager, r *run.Run, session string) (*tui.Writer, func(), io.Writer) {
	stdout := io.Writer(os.Stdout)
	cleanup := func() {}
	if !term.IsTerminal(os.Stdout) {
		return nil, cleanup, stdout
	}
	width, height := term.GetSize(os.Stdout)
	if width <= 0 || height <= 0 {
		return nil, cleanup, stdout
	}
	runtimeType := r.Runtime
	if runtimeType == "" {
		runtimeType = manager.RuntimeType()
	}
	bar := tui.NewStatusBar(r.ID, r.Name, runtimeType)
	bar.SetGrants(r.Grants)
	bar.SetSession(session)
	bar.SetDimensions(width, height)
	writer := tui.NewWriter(os.Stdout, bar, runtimeType)
	if err := writer.Setup(); err != nil {
		return nil, cleanup, os.Stdout
	}
	_ = os.Stdout.Sync()
	cleanup = func() { _ = writer.Cleanup() }
	return writer, cleanup, writer
}

// resizePump forwards terminal-size updates to out until done is closed, then
// closes out. It owns out exclusively (sole sender + closer), so callers must
// NOT close out themselves. onWinch is called for each SIGWINCH (e.g. to resize
// the footer + read the new size); it returns the size to forward and whether to.
func resizePump(done <-chan struct{}, sigCh <-chan os.Signal, onWinch func() (container.TTYSize, bool), out chan<- container.TTYSize) {
	defer close(out)
	for {
		select {
		case <-done:
			return
		case sig, ok := <-sigCh:
			if !ok {
				return
			}
			if sig != syscall.SIGWINCH {
				continue
			}
			if size, forward := onWinch(); forward {
				select {
				case out <- size:
				default:
				}
			}
		}
	}
}

func runJoinInteractive(ctx context.Context, manager *run.Manager, r *run.Run, command []string, index int, registered bool) error {
	// Raw mode so keystrokes reach the agent unbuffered.
	var rawState *term.RawModeState
	if term.IsTerminal(os.Stdin) {
		if rs, rErr := term.EnableRawMode(os.Stdin); rErr == nil {
			rawState = rs
		}
	}
	defer func() {
		if rawState != nil {
			_ = term.RestoreTerminal(rawState)
		}
	}()

	// Status footer: show index only when registration succeeded.
	session := "joined"
	if registered {
		session = fmt.Sprintf("joined · %d", index)
	}
	statusWriter, statusCleanup, stdout := setupJoinStatusBar(manager, r, session)
	defer statusCleanup()

	// Tee join output to its own indexed log file only when registration succeeded,
	// kept separate from the primary's logs.jsonl per the split-console design.
	if registered && r.Store != nil {
		if lw, lerr := r.Store.JoinLogWriter(index); lerr == nil {
			defer lw.Close()
			stdout = io.MultiWriter(stdout, lw)
		}
	}

	// Resize channel owned by resizePump — do NOT close it here.
	resize := make(chan container.TTYSize, 1)
	var initialW, initialH uint
	if term.IsTerminal(os.Stdout) {
		if w, h := term.GetSize(os.Stdout); w > 0 && h > 0 {
			// Reserve the footer row.
			ch := containerTTYHeight(statusWriter, h)
			initialW, initialH = uint(w), uint(ch) // #nosec G115
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	done := make(chan struct{})

	// Restore the terminal if we're asked to terminate while in raw mode. An
	// external SIGTERM/SIGHUP would otherwise kill the process before the
	// deferred RestoreTerminal runs, leaving the terminal garbled. Canceling
	// execCtx unblocks ExecInteractive so the deferred cleanup runs normally.
	termCh := make(chan os.Signal, 1)
	signal.Notify(termCh, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(termCh)
	go func() {
		select {
		case <-termCh:
			cancel()
		case <-done:
		}
	}()

	onWinch := func() (container.TTYSize, bool) {
		if statusWriter == nil || !term.IsTerminal(os.Stdout) {
			return container.TTYSize{}, false
		}
		w, h := term.GetSize(os.Stdout)
		if w <= 0 || h <= 0 {
			return container.TTYSize{}, false
		}
		_ = statusWriter.Resize(w, h)
		ch := containerTTYHeight(statusWriter, h)
		return container.TTYSize{Width: uint(w), Height: uint(ch)}, true // #nosec G115
	}
	go resizePump(done, sigCh, onWinch, resize)

	execErr := manager.ExecInteractive(execCtx, r.ID, command, container.ExecOptions{
		Stdin:         os.Stdin,
		Stdout:        stdout,
		Stderr:        os.Stderr,
		TTY:           true,
		InitialWidth:  initialW,
		InitialHeight: initialH,
		Resize:        resize,
	})
	// Signal resizePump to stop; it closes resize, which unblocks the runtime side.
	close(done)

	// A canceled execCtx means we were signaled to terminate (above) — not a
	// failure; fall through to the clean exit so the terminal is restored.
	if execErr != nil && ctx.Err() == nil && !errors.Is(execErr, context.Canceled) {
		var ee *container.ExecError
		if errors.As(execErr, &ee) {
			// Agent exited non-zero; surface it but don't treat as a moat error.
			fmt.Printf("\r\nJoined agent exited (code %d)\r\n", ee.ExitCode)
			return nil
		}
		return fmt.Errorf("join failed: %w", execErr)
	}
	fmt.Printf("\r\nJoined agent session ended\r\n")
	return nil
}
