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
	// Do NOT defer release here — we call it explicitly before exitWithExecError
	// so registry cleanup runs even when the agent exits with a non-zero code.
	index, release, regErr := manager.RegisterJoinedAgent(runID)

	var execErr error
	// Headless (--prompt with no TTY) vs interactive.
	if joinPrompt != "" || !term.IsTerminal(os.Stdin) || !term.IsTerminal(os.Stdout) {
		execErr = runJoinHeadless(ctx, manager, r, containerCmd, index, regErr == nil)
	} else {
		execErr = runJoinInteractive(ctx, manager, r, containerCmd, index, regErr == nil)
	}

	// Explicit cleanup before a possible os.Exit so the registry entry is
	// released and lw.Close() (inside the helpers) has already run.
	if regErr == nil {
		release()
	}

	return exitWithExecError(manager, execErr)
}

// runJoinHeadless runs the joined agent without a TTY, forwarding piped stdin
// when available. lw.Close() runs as the function returns, before runJoin
// calls exitWithExecError, so log flushing precedes any os.Exit.
func runJoinHeadless(ctx context.Context, manager *run.Manager, r *run.Run, command []string, index int, registered bool) error {
	var out io.Writer = os.Stdout
	if registered && r.Store != nil {
		if lw, lerr := r.Store.JoinLogWriter(index); lerr == nil {
			defer lw.Close()
			out = io.MultiWriter(os.Stdout, lw)
		}
	}

	// Forward piped stdin so "echo ... | moat join <run> claude" works.
	var stdin io.Reader
	if !term.IsTerminal(os.Stdin) {
		stdin = os.Stdin
	}

	return manager.ExecInteractive(ctx, r.ID, command, container.ExecOptions{
		Stdin:  stdin,
		Stdout: out,
		Stderr: os.Stderr,
		TTY:    false,
	})
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
	statusWriter, statusCleanup, stdout := setupStatusBar(manager, r, session)
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
	// Deferred RestoreTerminal and statusCleanup run before returning, ensuring
	// the terminal is in a good state before runJoin calls exitWithExecError.
	if execErr != nil && !errors.Is(execErr, context.Canceled) {
		var ee *container.ExecError
		if errors.As(execErr, &ee) {
			// Return the ExecError so runJoin can propagate the exit code.
			return ee
		}
		return fmt.Errorf("join failed: %w", execErr)
	}
	fmt.Printf("\r\nJoined agent session ended\r\n")
	return nil
}
