package codex

import (
	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
)

var (
	codexFlags        cli.ExecFlags
	codexPromptFlag   string
	codexAllowedHosts []string
	codexFullAuto     bool
	codexWtFlag       string
)

// NetworkHosts returns the list of hosts that Codex needs network access to.
// These should be added to the network allow list for containers running Codex.
func NetworkHosts() []string {
	return []string{
		"api.openai.com",
		"*.openai.com",
		"auth.openai.com",
		"platform.openai.com",
		"chatgpt.com",
		"*.chatgpt.com",
	}
}

// DefaultDependencies returns the default dependencies for running Codex CLI.
func DefaultDependencies() []string {
	return []string{
		"node@20",
		"git",
		"codex-cli",
	}
}

// RegisterCLI registers Codex-related CLI commands.
// This adds the `moat codex` command group with subcommands.
func (p *Provider) RegisterCLI(root *cobra.Command) {
	codexCmd := &cobra.Command{
		Use:   "codex [workspace] [flags]",
		Short: "Run Codex CLI in an isolated container",
		Long: `Run OpenAI Codex CLI in an isolated container with automatic credential injection.

Your workspace is mounted at /workspace inside the container. API credentials
are injected transparently via the Moat proxy - Codex CLI never sees raw tokens.

By default, Codex runs with --full-auto mode enabled (auto-approves tool use).
Use --full-auto=false to require manual approval for each action.

Without a workspace argument, uses the current directory.

Examples:
  # Start Codex CLI in current directory (interactive)
  moat codex

  # Start Codex CLI in a specific project
  moat codex ./my-project

  # Start with an initial prompt (interactive - Codex stays open)
  moat codex -- "testing"
  moat codex ./my-project -- "explain this codebase"

  # Ask Codex to do something specific (non-interactive)
  moat codex -p "explain this codebase"
  moat codex -p "fix the bug in main.py"

  # Add additional grants (e.g., for GitHub API access)
  moat codex --grant github

  # Name the session for easy reference
  moat codex --name my-feature

  # Force rebuild of container image
  moat codex --rebuild

  # Disable full-auto mode (require manual approval)
  moat codex --full-auto=false

Use 'moat list' to see running and recent runs.`,
		Args: cobra.ArbitraryArgs,
		RunE: runCodex,
	}

	// Add shared execution flags
	cli.AddExecFlags(codexCmd, &codexFlags)

	// Add Codex-specific flags
	codexCmd.Flags().StringVarP(&codexPromptFlag, "prompt", "p", "", "run with prompt (non-interactive mode)")
	codexCmd.Flags().StringSliceVar(&codexAllowedHosts, "allow-host", nil, "additional hosts to allow network access to")
	codexCmd.Flags().BoolVar(&codexFullAuto, "full-auto", true, "enable full-auto mode (auto-approve tool use); set to false for manual approval")
	codexCmd.Flags().StringVar(&codexWtFlag, "worktree", "", "run in a git worktree for this branch")
	codexCmd.Flags().StringVar(&codexWtFlag, "wt", "", "alias for --worktree")
	_ = codexCmd.Flags().MarkHidden("wt")

	root.AddCommand(codexCmd)
}

func runCodex(cmd *cobra.Command, args []string) error {
	return cli.RunProvider(cmd, args, cli.ProviderRunConfig{
		Name:                  "codex",
		Flags:                 &codexFlags,
		PromptFlag:            codexPromptFlag,
		AllowedHosts:          codexAllowedHosts,
		WtFlag:                codexWtFlag,
		GetCredentialGrant:    getCodexCredentialName,
		Dependencies:          DefaultDependencies(),
		NetworkHosts:          NetworkHosts(),
		SupportsInitialPrompt: true,
		DryRunNote:            "Note: No API key configured. Codex will prompt for login.",
		BuildCommand: func(promptFlag, initialPrompt string) ([]string, error) {
			if promptFlag != "" {
				// Non-interactive: use `codex exec` with the prompt.
				// --full-auto allows edits during execution (safe in a container).
				containerCmd := []string{"codex", "exec"}
				if codexFullAuto {
					containerCmd = append(containerCmd, "--full-auto")
				}
				containerCmd = append(containerCmd, promptFlag)
				return containerCmd, nil
			}
			// Interactive: run `codex` for the TUI
			containerCmd := []string{"codex"}
			if initialPrompt != "" {
				containerCmd = append(containerCmd, initialPrompt)
			}
			return containerCmd, nil
		},
		ConfigureAgent: func(cfg *config.Config) {
			syncLogs := true
			cfg.Codex.SyncLogs = &syncLogs
		},
	})
}

// getCodexCredentialName returns the name under which the Codex credential is stored.
// Returns empty string if no credential exists.
func getCodexCredentialName() string {
	// Check both provider names (codex is the internal name, openai is legacy)
	for _, name := range []string{"codex", "openai"} {
		key, err := credential.DefaultEncryptionKey()
		if err != nil {
			continue
		}
		store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
		if err != nil {
			continue
		}
		if _, err := store.Get(credential.Provider(name)); err == nil {
			return name
		}
	}
	return ""
}
