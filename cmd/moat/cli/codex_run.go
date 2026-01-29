package cli

import (
	"context"
	"fmt"

	"github.com/majorcontext/moat/internal/codex"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/run"
	"github.com/spf13/cobra"
)

var (
	codexFlags        ExecFlags
	codexPromptFlag   string
	codexAllowedHosts []string
	codexFullAuto     bool
)

func init() {
	// Add the run functionality directly to codexCmd
	codexCmd.RunE = runCodex
	codexCmd.Args = cobra.MaximumNArgs(1)

	// Update usage
	codexCmd.Use = "codex [workspace] [flags]"

	// Add shared execution flags
	AddExecFlags(codexCmd, &codexFlags)

	// Add Codex-specific flags
	codexCmd.Flags().StringVarP(&codexPromptFlag, "prompt", "p", "", "run with prompt (non-interactive mode)")
	codexCmd.Flags().StringSliceVar(&codexAllowedHosts, "allow-host", nil, "additional hosts to allow network access to")
	codexCmd.Flags().BoolVar(&codexFullAuto, "full-auto", true, "enable full-auto mode (auto-approve tool use); set to false for manual approval")
}

func runCodex(cmd *cobra.Command, args []string) error {
	// If subcommand is being run, don't execute this
	if cmd.CalledAs() != "codex" || len(args) > 0 && args[0] == "sessions" {
		return nil
	}

	// Determine workspace
	workspace := "."
	if len(args) > 0 {
		workspace = args[0]
	}

	absPath, err := resolveWorkspacePath(workspace)
	if err != nil {
		return err
	}

	// Load agent.yaml if present, otherwise use defaults
	cfg, err := config.Load(absPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Build grants list using a set for deduplication
	// If user has an API key stored via `moat grant openai`, use proxy injection
	// Otherwise, Codex will prompt for login on first run
	grantSet := make(map[string]bool)
	var grants []string
	addGrant := func(g string) error {
		if grantSet[g] {
			return nil // Already added
		}
		if validateErr := credential.ValidateGrant(g); validateErr != nil {
			return fmt.Errorf("invalid grant %q: %w", g, validateErr)
		}
		grantSet[g] = true
		grants = append(grants, g)
		return nil
	}

	if hasCredential(credential.ProviderOpenAI) {
		_ = addGrant("openai") // Known valid grant
	}
	if cfg != nil {
		for _, g := range cfg.Grants {
			if grantErr := addGrant(g); grantErr != nil {
				return grantErr
			}
		}
	}
	for _, g := range codexFlags.Grants {
		if grantErr := addGrant(g); grantErr != nil {
			return grantErr
		}
	}
	codexFlags.Grants = grants

	// Determine interactive mode
	interactive := codexPromptFlag == ""

	// Build container command
	// codex is installed globally via the dependency system
	var containerCmd []string

	if codexPromptFlag != "" {
		// Non-interactive mode: use `codex exec` with the prompt
		// --full-auto allows edits during execution (safe since we're in a container)
		containerCmd = []string{"codex", "exec"}
		if codexFullAuto {
			containerCmd = append(containerCmd, "--full-auto")
		}
		containerCmd = append(containerCmd, codexPromptFlag)
	} else {
		// Interactive mode: just run `codex` for the TUI
		containerCmd = []string{"codex"}
	}

	// Use name from flag, or config, or let manager generate one
	if codexFlags.Name == "" && cfg != nil && cfg.Name != "" {
		codexFlags.Name = cfg.Name
	}

	// Ensure dependencies for Codex CLI
	if cfg == nil {
		cfg = &config.Config{}
	}
	if !hasDependency(cfg.Dependencies, "node") {
		cfg.Dependencies = append(cfg.Dependencies, "node@20")
	}
	if !hasDependency(cfg.Dependencies, "git") {
		cfg.Dependencies = append(cfg.Dependencies, "git")
	}
	if !hasDependency(cfg.Dependencies, "codex-cli") {
		cfg.Dependencies = append(cfg.Dependencies, "codex-cli")
	}

	// Always sync Codex logs for moat codex command
	syncLogs := true
	cfg.Codex.SyncLogs = &syncLogs

	// Allow network access to OpenAI for API access and auth
	// chatgpt.com is needed for subscription token validation
	cfg.Network.Allow = append(cfg.Network.Allow,
		"api.openai.com",
		"*.openai.com",
		"auth.openai.com",
		"platform.openai.com",
		"chatgpt.com",
		"*.chatgpt.com",
	)

	// Add allowed hosts if specified, with validation
	for _, host := range codexAllowedHosts {
		if hostErr := validateHost(host); hostErr != nil {
			return fmt.Errorf("invalid --allow-host %q: %w", host, hostErr)
		}
		cfg.Network.Allow = append(cfg.Network.Allow, host)
	}

	// Add environment variables from flags
	if envErr := parseEnvFlags(codexFlags.Env, cfg); envErr != nil {
		return envErr
	}

	log.Debug("starting codex",
		"workspace", absPath,
		"grants", grants,
		"interactive", interactive,
		"prompt", codexPromptFlag,
		"rebuild", codexFlags.Rebuild,
	)

	if dryRun {
		fmt.Println("Dry run - would start Codex")
		fmt.Printf("Workspace: %s\n", absPath)
		fmt.Printf("Grants: %v\n", grants)
		fmt.Printf("Interactive: %v\n", interactive)
		fmt.Printf("Rebuild: %v\n", codexFlags.Rebuild)
		if len(grants) == 0 {
			fmt.Println("Note: No API key configured. Codex will prompt for login.")
		}
		return nil
	}

	ctx := context.Background()

	opts := ExecOptions{
		Flags:       codexFlags,
		Workspace:   absPath,
		Command:     containerCmd,
		Config:      cfg,
		Interactive: interactive,
		TTY:         interactive,
		OnRunCreated: func(r *run.Run) {
			// Create session record
			sessionMgr, sessionErr := codex.NewSessionManager()
			if sessionErr != nil {
				log.Debug("failed to create session manager", "error", sessionErr)
				return
			}
			if _, sessionErr = sessionMgr.Create(absPath, r.ID, r.Name, grants); sessionErr != nil {
				log.Debug("failed to create session", "error", sessionErr)
			}
		},
	}

	r, err := ExecuteRun(ctx, opts)
	if err != nil {
		return err
	}

	if r != nil {
		if codexFlags.Detach {
			fmt.Printf("Started Codex in background: %s (run %s)\n", r.Name, r.ID)
			fmt.Printf("  Workspace: %s\n", absPath)
			fmt.Printf("  Attach with: moat attach %s\n", r.ID)
		} else {
			fmt.Printf("Starting Codex in %s\n", absPath)
			fmt.Printf("Session: %s (run %s)\n", r.Name, r.ID)
		}
	}

	return nil
}
