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
	nameFlag    string
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

Examples:
  # Run from current directory (uses agent.yaml config)
  agent run

  # Run from a specific directory
  agent run ./my-project

  # Run with a specific name for hostname routing
  agent run --name myapp ./my-project

  # Run with a Python runtime
  agent run --runtime python:3.11

  # Run with Node.js runtime and custom command
  agent run --runtime node:20 -- npm test

  # Run with GitHub credentials
  agent run --grant github

  # Run with environment variables
  agent run -e DEBUG=true -e API_KEY=xxx

  # Run multiple commands
  agent run -- sh -c "npm install && npm test"`,
	Args: cobra.ArbitraryArgs,
	RunE: runAgent,
}

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().StringSliceVar(&grants, "grant", nil, "capabilities to grant (e.g., github, aws:s3.read)")
	runCmd.Flags().StringArrayVarP(&runEnv, "env", "e", nil, "environment variables (KEY=VALUE)")
	runCmd.Flags().StringVar(&runtimeFlag, "runtime", "", "runtime language:version (e.g., python:3.11, node:20, go:1.22)")
	runCmd.Flags().StringVar(&nameFlag, "name", "", "name for this agent instance (default: from agent.yaml or random)")
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
	}

	log.Debug("preparing run",
		"name", agentInstanceName,
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
		Name:      agentInstanceName,
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

	log.Info("created run", "id", r.ID, "name", r.Name)

	// Start run
	if err := manager.Start(ctx, r.ID); err != nil {
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
			url := fmt.Sprintf("http://%s.%s.localhost:%d", serviceName, r.Name, proxyPort)
			fmt.Printf("  %s: %s (container :%d)\n", serviceName, url, containerPort)
		}
	}
	fmt.Println()

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
