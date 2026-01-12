package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/andybons/agentops/internal/config"
	"github.com/andybons/agentops/internal/log"
	"github.com/andybons/agentops/internal/run"
	"github.com/spf13/cobra"
)

var (
	grants      []string
	runEnv      []string
	runtimeFlag string
)

var runCmd = &cobra.Command{
	Use:   "run <agent> [path] [-- command]",
	Short: "Run an agent in an isolated environment",
	Long: `Run an agent in an isolated container with workspace mounting,
credential injection, and full observability.

The agent runs in a Docker container with your workspace mounted at /workspace.
If an agent.yaml exists in the workspace, its settings are used as defaults.

Arguments:
  <agent>      Name of the agent to run (a label for this run)
  [path]       Path to workspace directory (default: current directory)
  [-- cmd]     Optional command to run instead of agent's default

Examples:
  # Run an agent with a Python runtime
  agent run my-agent . --runtime python:3.11

  # Run with Node.js runtime and custom command
  agent run my-agent . --runtime node:20 -- npm test

  # Run with Go runtime
  agent run my-agent . --runtime go:1.22

  # Run with GitHub credentials
  agent run my-agent . --grant github

  # Run with multiple grants
  agent run my-agent . --grant github --grant aws:s3.read

  # Run with environment variables
  agent run my-agent . -e DEBUG=true -e API_KEY=xxx

  # Run multiple commands
  agent run my-agent . -- sh -c "npm install && npm test"`,
	Args: cobra.MinimumNArgs(1),
	RunE: runAgent,
}

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().StringSliceVar(&grants, "grant", nil, "capabilities to grant (e.g., github, aws:s3.read)")
	runCmd.Flags().StringArrayVarP(&runEnv, "env", "e", nil, "environment variables (KEY=VALUE)")
	runCmd.Flags().StringVar(&runtimeFlag, "runtime", "", "runtime language:version (e.g., python:3.11, node:20, go:1.22)")
}

func runAgent(cmd *cobra.Command, args []string) error {
	agentName := args[0]

	// Parse args: <agent> [path] [-- command...]
	workspacePath := "."
	var containerCmd []string

	// Check if there's a -- separator
	dashIdx := cmd.ArgsLenAtDash()
	if dashIdx >= 0 {
		// Args before -- are agent and path
		if dashIdx > 1 {
			workspacePath = args[1]
		}
		// Args after -- are the command
		containerCmd = args[dashIdx:]
	} else {
		// No --, so second arg (if present) is path
		if len(args) > 1 {
			workspacePath = args[1]
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

	// Handle --runtime flag (overrides agent.yaml runtime)
	if runtimeFlag != "" {
		rt, rtErr := config.ParseRuntime(runtimeFlag)
		if rtErr != nil {
			return rtErr
		}
		if cfg == nil {
			cfg = config.DefaultConfig()
		}
		cfg.Runtime = *rt
	}

	// Apply config defaults
	if cfg != nil {
		if agentName == "" && cfg.Agent != "" {
			agentName = cfg.Agent
		}
		if len(grants) == 0 && len(cfg.Grants) > 0 {
			grants = cfg.Grants
		}
	}

	log.Debug("preparing run",
		"agent", agentName,
		"workspace", absPath,
		"grants", grants,
		"cmd", containerCmd,
	)

	if dryRun {
		fmt.Println("Dry run - would start agent container")
		if len(containerCmd) > 0 {
			fmt.Printf("Command: %v\n", containerCmd)
		}
		return nil
	}

	// Set up context with signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	// Create manager
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	opts := run.Options{
		Agent:     agentName,
		Workspace: absPath,
		Grants:    grants,
		Cmd:       containerCmd,
		Config:    cfg,
		Env:       runEnv,
	}

	// Create run
	r, err := manager.Create(ctx, opts)
	if err != nil {
		return fmt.Errorf("creating run: %w", err)
	}

	log.Info("created run", "id", r.ID, "agent", agentName)
	fmt.Printf("Created run: %s\n", r.ID)

	// Start run
	if err := manager.Start(ctx, r.ID); err != nil {
		log.Error("failed to start run", "id", r.ID, "error", err)
		return fmt.Errorf("starting run: %w", err)
	}

	log.Info("run started", "id", r.ID)
	fmt.Printf("Run %s started\n", r.ID)

	// Wait for completion or interrupt
	if err := manager.Wait(ctx, r.ID); err != nil {
		if ctx.Err() != nil {
			// Context was canceled (signal received)
			log.Info("run stopped by user", "id", r.ID)
			fmt.Printf("\nRun %s stopped\n", r.ID)
			return nil
		}
		log.Error("run failed", "id", r.ID, "error", err)
		return err
	}

	log.Info("run completed", "id", r.ID)
	fmt.Printf("Run %s completed\n", r.ID)
	return nil
}
