package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	intcli "github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/ui"
	"github.com/spf13/cobra"
)

var runFlags ExecFlags

var runCmd = &cobra.Command{
	Use:   "run [path] [-- command]",
	Short: "Run an agent in an isolated environment",
	Long: `Run an agent in an isolated container with workspace mounting,
credential injection, and full observability.

The agent runs in a container with your workspace mounted at /workspace.
If a moat.yaml exists in the workspace, its settings are used as defaults.

Arguments:
  [path]       Path to workspace directory (default: current directory)
  [-- cmd]     Optional command to run instead of agent's default

Non-interactive mode (default):
  Output streams to the terminal. Press Ctrl+C to stop.

Interactive mode (-i):
  Ctrl-/ k          Stop the run
  Ctrl+C            Sent to container process

Examples:
  # Run from current directory (uses moat.yaml config)
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

  # Run interactive shell
  moat run -i -- bash`,
	Args: cobra.ArbitraryArgs,
	RunE: runAgent,
}

func init() {
	rootCmd.AddCommand(runCmd)
	AddExecFlags(runCmd, &runFlags)
	runCmd.Flags().BoolVarP(&runFlags.Interactive, "interactive", "i", false, "interactive mode (stdin + TTY)")
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

	// Load moat.yaml if present
	cfg, err := config.Load(absPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Determine agent name: --name flag > config.Name > random
	if runFlags.Name == "" && cfg != nil && cfg.Name != "" {
		runFlags.Name = cfg.Name
	}
	// Random name generation happens in manager.Create if still empty

	// Apply config defaults: agents: expansion, grants, and command. See
	// intcli.ApplyAgentDefaults for the ExpandAgents → grants-merge →
	// AppendDerivedGrants ordering invariant this preserves.
	if err = intcli.ApplyAgentDefaults(cfg, &runFlags.Grants, &containerCmd); err != nil {
		return err
	}
	if cfg != nil {
		// Check sandbox setting from config
		if cfg.Sandbox == "none" && !runFlags.NoSandbox {
			runFlags.NoSandbox = true
		}
	}

	// Determine interactive mode: CLI flags > config > default
	interactive := runFlags.Interactive
	if !interactive && cfg != nil && cfg.Interactive {
		interactive = true
	}

	// Warn if command looks interactive but -i wasn't specified
	if !interactive && len(containerCmd) > 0 {
		cmdName := containerCmd[0]
		// Check for common interactive commands
		if cmdName == "bash" || cmdName == "sh" || cmdName == "zsh" ||
			cmdName == "/bin/bash" || cmdName == "/bin/sh" || cmdName == "/bin/zsh" {
			// Only warn if no additional args (bare shell invocation)
			if len(containerCmd) == 1 {
				ui.Infof("Hint: '%s' is interactive. Consider using: moat run -i -- %s", cmdName, cmdName)
			}
		}
	}

	// moat run has no verb, so a valid agent: is preserved and an invalid one
	// warns and is cleared. This runs BEFORE the dry-run return below: --dry-run
	// is what someone reaches for to check a moat.yaml, so it must surface the
	// same agent: warnings a real run would.
	intcli.ResolveAgentField(cfg, "")

	log.Debug("preparing run",
		"name", runFlags.Name,
		"workspace", absPath,
		"grants", runFlags.Grants,
		"cmd", containerCmd,
		"interactive", interactive,
	)

	if dryRun {
		fmt.Println("Dry run - would start agent container")
		if len(containerCmd) > 0 {
			fmt.Printf("Command: %v\n", containerCmd)
		}
		return nil
	}

	ctx := context.Background()

	opts := ExecOptions{
		Flags:       runFlags,
		Workspace:   absPath,
		Command:     containerCmd,
		Config:      cfg,
		Interactive: interactive,
	}

	_, err = ExecuteRun(ctx, opts)
	return err
}
