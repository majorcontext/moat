package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/term"
	"github.com/spf13/cobra"
)

var execCmd = &cobra.Command{
	Use:   "exec <run> -- <command> [args...]",
	Short: "Run a command in a running container",
	Long: `Execute a command inside a running container.

The run can be specified by ID or name. Use -- to separate moat flags
from the command to execute.

Examples:
  moat exec run_a1b2c3d4e5f6 -- echo hello
  moat exec run_a1b2c3d4e5f6 -- ls /workspace
  echo "data" | moat exec run_a1b2c3d4e5f6 -- cat
  moat exec run_a1b2c3d4e5f6 -- sh -c "ps aux"`,
	Args:               cobra.MinimumNArgs(1),
	RunE:               runExec,
	DisableFlagParsing: false,
}

func init() {
	rootCmd.AddCommand(execCmd)
}

func runExec(cmd *cobra.Command, args []string) error {
	// Split args at "--": everything before is the run identifier,
	// everything after is the command. Cobra strips the first "--" for us,
	// so args is [run, cmd...] when the user writes: moat exec <run> -- <cmd>
	if len(args) < 2 {
		return fmt.Errorf("usage: moat exec <run> -- <command> [args...]")
	}

	runArg := args[0]
	execArgs := args[1:]

	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	runID, err := resolveRunArgSingle(manager, runArg)
	if err != nil {
		return err
	}

	// Read stdin if it's not a terminal (piped input)
	var stdin []byte
	if !term.IsTerminal(os.Stdin) {
		stdin, err = io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("reading stdin: %w", err)
		}
	}

	ctx := context.Background()
	execErr := manager.Exec(ctx, runID, execArgs, stdin, os.Stdout, os.Stderr)
	if execErr != nil {
		var ee *container.ExecError
		if errors.As(execErr, &ee) {
			os.Exit(ee.ExitCode)
		}
		return execErr
	}
	return nil
}
