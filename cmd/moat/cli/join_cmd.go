package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
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
		return fmt.Errorf("unknown agent %q", agentArg)
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

	return runJoinInteractive(ctx, manager, r, containerCmd, index)
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

func setupJoinStatusBar(manager *run.Manager, r *run.Run, index int) (*tui.Writer, func(), io.Writer) {
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
	bar.SetSession(fmt.Sprintf("joined · %d", index))
	bar.SetDimensions(width, height)
	writer := tui.NewWriter(os.Stdout, bar, runtimeType)
	if err := writer.Setup(); err != nil {
		return nil, cleanup, os.Stdout
	}
	_ = os.Stdout.Sync()
	cleanup = func() { _ = writer.Cleanup() }
	return writer, cleanup, writer
}

func runJoinInteractive(ctx context.Context, manager *run.Manager, r *run.Run, command []string, index int) error {
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

	// Status footer: this is a joined session.
	statusWriter, statusCleanup, stdout := setupJoinStatusBar(manager, r, index)
	defer statusCleanup()

	// Tee join output to its own indexed log file, kept separate from the
	// primary's logs.jsonl per the split-console design.
	if r.Store != nil {
		if lw, lerr := r.Store.JoinLogWriter(index); lerr == nil {
			defer lw.Close()
			stdout = io.MultiWriter(stdout, lw)
		}
	}

	// Resize channel fed by SIGWINCH; closed on exit so the runtime goroutine ends.
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

	go func() {
		for sig := range sigCh {
			if sig != syscall.SIGWINCH {
				continue
			}
			if statusWriter != nil && term.IsTerminal(os.Stdout) {
				if w, h := term.GetSize(os.Stdout); w > 0 && h > 0 {
					_ = statusWriter.Resize(w, h)
					ch := containerTTYHeight(statusWriter, h)
					select {
					case resize <- container.TTYSize{Width: uint(w), Height: uint(ch)}: // #nosec G115
					default:
					}
				}
			}
		}
	}()

	execErr := manager.ExecInteractive(execCtx, r.ID, command, container.ExecOptions{
		Stdin:         os.Stdin,
		Stdout:        stdout,
		Stderr:        os.Stderr,
		TTY:           true,
		InitialWidth:  initialW,
		InitialHeight: initialH,
		Resize:        resize,
	})
	close(resize)

	if execErr != nil && ctx.Err() == nil {
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
