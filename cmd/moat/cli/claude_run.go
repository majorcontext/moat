package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/andybons/moat/internal/claude"
	"github.com/andybons/moat/internal/config"
	"github.com/andybons/moat/internal/credential"
	"github.com/andybons/moat/internal/log"
	"github.com/andybons/moat/internal/run"
	"github.com/spf13/cobra"
)

var (
	claudeFlags        ExecFlags
	claudePromptFlag   string
	claudeAllowedHosts []string
)

func init() {
	// Add the run functionality directly to claudeCmd
	claudeCmd.RunE = runClaudeCode
	claudeCmd.Args = cobra.MaximumNArgs(1)

	// Update usage and examples
	claudeCmd.Use = "claude [workspace] [flags]"
	claudeCmd.Long = `Run Claude Code in an isolated container with automatic credential injection.

Your workspace is mounted at /workspace inside the container. API credentials
are injected transparently via the Moat proxy - Claude Code never sees raw tokens.

Without a workspace argument, uses the current directory.

Examples:
  # Start Claude Code in current directory (interactive)
  moat claude

  # Start Claude Code in a specific project
  moat claude ./my-project

  # Ask Claude to do something specific (non-interactive)
  moat claude -p "explain this codebase"
  moat claude -p "fix the bug in main.py"

  # Add additional grants (e.g., for GitHub API access)
  moat claude --grant github

  # Name the session for easy reference
  moat claude --name my-feature

  # Run in background
  moat claude -d

  # Force rebuild of container image
  moat claude --rebuild

Subcommands:
  moat claude plugins list         List configured plugins
  moat claude marketplace list     List marketplaces
  moat claude marketplace update   Update marketplace caches`

	// Add shared execution flags
	AddExecFlags(claudeCmd, &claudeFlags)

	// Add Claude-specific flags
	claudeCmd.Flags().StringVarP(&claudePromptFlag, "prompt", "p", "", "run with prompt (non-interactive mode)")
	claudeCmd.Flags().StringSliceVar(&claudeAllowedHosts, "allow-host", nil, "additional hosts to allow network access to")
}

func runClaudeCode(cmd *cobra.Command, args []string) error {
	// If subcommand is being run, don't execute this
	if cmd.CalledAs() != "claude" || len(args) > 0 && (args[0] == "plugins" || args[0] == "marketplace") {
		return nil
	}

	// Determine workspace
	workspace := "."
	if len(args) > 0 {
		workspace = args[0]
	}

	absPath, err := filepath.Abs(workspace)
	if err != nil {
		return fmt.Errorf("resolving workspace path: %w", err)
	}

	// Verify path exists and is a directory
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("workspace path %q: %w", absPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace path %q is not a directory", absPath)
	}

	// Load agent.yaml if present, otherwise use defaults
	cfg, err := config.Load(absPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Build grants list using a set for deduplication
	// If user has an API key stored via `moat grant anthropic`, use proxy injection
	// Otherwise, Claude will prompt for login on first run (Pro/Max subscription)
	grantSet := make(map[string]bool)
	var grants []string
	addGrant := func(g string) {
		if !grantSet[g] {
			grantSet[g] = true
			grants = append(grants, g)
		}
	}

	if hasAnthropicCredential() {
		addGrant("anthropic")
	}
	if cfg != nil {
		for _, g := range cfg.Grants {
			addGrant(g)
		}
	}
	for _, g := range claudeFlags.Grants {
		addGrant(g)
	}
	claudeFlags.Grants = grants

	// Build container command
	// claude-code is installed globally via the dependency system
	containerCmd := []string{"claude"}
	if claudePromptFlag != "" {
		containerCmd = append(containerCmd, "-p", claudePromptFlag)
	}

	// Determine interactive mode
	interactive := claudePromptFlag == ""

	// Use name from flag, or config, or let manager generate one
	if claudeFlags.Name == "" && cfg != nil && cfg.Name != "" {
		claudeFlags.Name = cfg.Name
	}

	// Ensure dependencies for Claude Code
	if cfg == nil {
		cfg = &config.Config{}
	}
	if !hasDependency(cfg.Dependencies, "node") {
		cfg.Dependencies = append(cfg.Dependencies, "node@20")
	}
	if !hasDependency(cfg.Dependencies, "git") {
		cfg.Dependencies = append(cfg.Dependencies, "git")
	}
	if !hasDependency(cfg.Dependencies, "claude-code") {
		cfg.Dependencies = append(cfg.Dependencies, "claude-code")
	}

	// Always sync Claude logs for moat claude command
	syncLogs := true
	cfg.Claude.SyncLogs = &syncLogs

	// Allow network access to claude.ai for OAuth login
	// Note: We don't mount host credentials because OAuth tokens are tied to
	// the host machine and don't transfer cleanly to containers. Users will
	// need to run `claude` and login on first use of a new container.
	cfg.Network.Allow = append(cfg.Network.Allow, "claude.ai", "*.claude.ai")

	// Add allowed hosts if specified
	cfg.Network.Allow = append(cfg.Network.Allow, claudeAllowedHosts...)

	// Add environment variables from flags
	if len(claudeFlags.Env) > 0 {
		// Merge into config (env from flags takes precedence)
		if cfg.Env == nil {
			cfg.Env = make(map[string]string)
		}
		for _, e := range claudeFlags.Env {
			key, value, ok := strings.Cut(e, "=")
			if !ok {
				return fmt.Errorf("invalid environment variable %q: expected KEY=VALUE format", e)
			}
			cfg.Env[key] = value
		}
	}

	log.Debug("starting claude code",
		"workspace", absPath,
		"grants", grants,
		"interactive", interactive,
		"prompt", claudePromptFlag,
		"rebuild", claudeFlags.Rebuild,
	)

	if dryRun {
		fmt.Println("Dry run - would start Claude Code")
		fmt.Printf("Workspace: %s\n", absPath)
		fmt.Printf("Grants: %v\n", grants)
		fmt.Printf("Interactive: %v\n", interactive)
		fmt.Printf("Rebuild: %v\n", claudeFlags.Rebuild)
		if len(grants) == 0 {
			fmt.Println("Note: No API key configured. Claude will prompt for login.")
		}
		return nil
	}

	ctx := context.Background()

	opts := ExecOptions{
		Flags:       claudeFlags,
		Workspace:   absPath,
		Command:     containerCmd,
		Config:      cfg,
		Interactive: interactive,
		TTY:         interactive,
		OnRunCreated: func(r *run.Run) {
			// Create session record
			sessionMgr, sessionErr := claude.NewSessionManager()
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

	if r != nil && !claudeFlags.Detach {
		fmt.Printf("Starting Claude Code in %s\n", absPath)
		fmt.Printf("Session: %s (run %s)\n", r.Name, r.ID)
	}

	return nil
}

// hasAnthropicCredential checks if an anthropic credential is stored via Moat grant.
func hasAnthropicCredential() bool {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		log.Debug("no anthropic credential: failed to get encryption key", "error", err)
		return false
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		log.Debug("no anthropic credential: failed to open credential store", "error", err)
		return false
	}
	_, err = store.Get(credential.ProviderAnthropic)
	if err != nil {
		log.Debug("no anthropic credential: not found in store", "error", err)
		return false
	}
	return true
}

// hasDependency checks if a dependency prefix exists in the list.
func hasDependency(deps []string, prefix string) bool {
	for _, d := range deps {
		if d == prefix || strings.HasPrefix(d, prefix+"@") {
			return true
		}
	}
	return false
}
