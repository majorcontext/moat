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
	codexNoYolo       bool
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
		"node@22",
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

By default, Codex runs with approvals off and its own sandbox disabled, since
the container is already the isolation boundary. Use --noyolo to restore Codex's
own approval prompts and sandbox.

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

  # Require manual approval for each action
  moat codex --noyolo

Use 'moat list' to see running and recent runs.`,
		Args: cobra.ArbitraryArgs,
		RunE: runCodex,
	}

	// Add shared execution flags
	cli.AddExecFlags(codexCmd, &codexFlags)

	// Add Codex-specific flags
	codexCmd.Flags().StringVarP(&codexPromptFlag, "prompt", "p", "", "run with prompt (non-interactive mode)")
	codexCmd.Flags().StringSliceVar(&codexAllowedHosts, "allow-host", nil, "additional hosts to allow network access to")
	codexCmd.Flags().BoolVar(&codexNoYolo, "noyolo", false, "keep Codex's own approval prompts and sandbox enabled (require manual approval for each action)")
	// Deprecated: --full-auto was removed from the Codex CLI (interactive) and
	// deprecated on `codex exec`. Kept hidden so existing scripts using
	// --full-auto=false still get manual approval; see runCodex.
	codexCmd.Flags().BoolVar(&codexFullAuto, "full-auto", true, "deprecated: use --noyolo to require manual approval")
	// MarkDeprecated warns on use and hides the flag from help.
	_ = codexCmd.Flags().MarkDeprecated("full-auto", "use --noyolo to require manual approval")
	codexCmd.Flags().StringVar(&codexWtFlag, "worktree", "", "run in a git worktree for this branch")
	codexCmd.Flags().StringVar(&codexWtFlag, "wt", "", "alias for --worktree")
	_ = codexCmd.Flags().MarkHidden("wt")

	root.AddCommand(codexCmd)
}

func runCodex(cmd *cobra.Command, args []string) error {
	// `--full-auto=false` was the old way to ask for manual approval. Cobra's
	// deprecation notice covers the rename; only the =false case changes
	// behavior, so map it onto --noyolo.
	if cmd.Flags().Changed("full-auto") && !codexFullAuto {
		codexNoYolo = true
	}

	return cli.RunProvider(cmd, args, cli.ProviderRunConfig{
		Name:                  "codex",
		Flags:                 &codexFlags,
		PromptFlag:            codexPromptFlag,
		AllowedHosts:          codexAllowedHosts,
		WtFlag:                codexWtFlag,
		GetCredentialGrant:    GetCredentialName,
		Dependencies:          DefaultDependencies(),
		NetworkHosts:          NetworkHosts(),
		SupportsInitialPrompt: true,
		DryRunNote:            "Note: No API key configured. Codex will prompt for login.",
		// Approval policy and sandbox mode are not passed as flags: they live in
		// the generated ~/.codex/config.toml (see NewConfig) so every way of
		// launching Codex in the container gets the same behavior, including a
		// custom `command:` in moat.yaml. CLI flags would also have to differ
		// between `codex` and `codex exec`, which accept different flags.
		BuildCommand: func(promptFlag, initialPrompt string) ([]string, error) {
			if promptFlag != "" {
				// Non-interactive: use `codex exec` with the prompt.
				return []string{"codex", "exec", promptFlag}, nil
			}
			// Interactive: run `codex` for the TUI
			containerCmd := []string{"codex"}
			if initialPrompt != "" {
				containerCmd = append(containerCmd, initialPrompt)
			}
			return containerCmd, nil
		},
		ConfigureAgent: func(cfg *config.Config) {
			cfg.Codex.RequireApproval = codexNoYolo
		},
	})
}

// GetCredentialName returns the name under which the Codex credential is stored.
// Returns empty string if no credential exists.
func GetCredentialName() string {
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
