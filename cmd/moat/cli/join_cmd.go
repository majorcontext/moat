package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

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
	if err := validateJoinAgent(joinable, agentArg, r.Agent); err != nil {
		return err
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

	// Headless (--prompt with no TTY) vs interactive.
	if joinPrompt != "" || !term.IsTerminal(os.Stdin) || !term.IsTerminal(os.Stdout) {
		execErr := manager.ExecInteractive(ctx, runID, containerCmd, container.ExecOptions{
			Stdin:  nil,
			Stdout: os.Stdout,
			Stderr: os.Stderr,
			TTY:    false,
		})
		return handleJoinExecErr(manager, execErr)
	}

	return runJoinInteractive(ctx, manager, r, containerCmd)
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

// runJoinInteractive is implemented in Task D2.
func runJoinInteractive(_ context.Context, _ *run.Manager, _ *run.Run, _ []string) error {
	return fmt.Errorf("interactive join not implemented")
}
