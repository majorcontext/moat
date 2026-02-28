package gemini

import (
	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
)

var (
	geminiFlags        cli.ExecFlags
	geminiPromptFlag   string
	geminiAllowedHosts []string
	geminiWtFlag       string
)

// NetworkHosts returns the list of hosts that Gemini needs network access to.
// These should be added to the network allow list for containers running Gemini.
func NetworkHosts() []string {
	return []string{
		"generativelanguage.googleapis.com",
		"*.googleapis.com",
		"oauth2.googleapis.com",
	}
}

// DefaultDependencies returns the default dependencies for running Gemini CLI.
func DefaultDependencies() []string {
	return []string{
		"node@20",
		"git",
		"gemini-cli",
	}
}

// RegisterCLI registers Gemini-related CLI commands.
// This adds the `moat gemini` command group with subcommands.
func (p *Provider) RegisterCLI(root *cobra.Command) {
	geminiCmd := &cobra.Command{
		Use:   "gemini [workspace] [flags]",
		Short: "Run Google Gemini CLI in an isolated container",
		Long: `Run Google Gemini CLI in an isolated container with automatic credential injection.

Your workspace is mounted at /workspace inside the container. API credentials
are injected transparently via the Moat proxy - Gemini never sees raw tokens.

Without a workspace argument, uses the current directory.

Examples:
  # Start Gemini in current directory (interactive)
  moat gemini

  # Start Gemini in a specific project
  moat gemini ./my-project

  # Ask Gemini to do something specific (non-interactive)
  moat gemini -p "explain this codebase"
  moat gemini -p "fix the bug in main.py"

  # Add additional grants (e.g., for GitHub API access)
  moat gemini --grant github

  # Name the session for easy reference
  moat gemini --name my-feature

  # Force rebuild of container image
  moat gemini --rebuild

Use 'moat list' to see running and recent runs.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runGemini,
	}

	// Add shared execution flags
	cli.AddExecFlags(geminiCmd, &geminiFlags)

	// Add Gemini-specific flags
	geminiCmd.Flags().StringVarP(&geminiPromptFlag, "prompt", "p", "", "run with prompt (non-interactive mode)")
	geminiCmd.Flags().StringSliceVar(&geminiAllowedHosts, "allow-host", nil, "additional hosts to allow network access to")
	geminiCmd.Flags().StringVar(&geminiWtFlag, "worktree", "", "run in a git worktree for this branch")
	geminiCmd.Flags().StringVar(&geminiWtFlag, "wt", "", "alias for --worktree")
	_ = geminiCmd.Flags().MarkHidden("wt")

	root.AddCommand(geminiCmd)
}

func runGemini(cmd *cobra.Command, args []string) error {
	return cli.RunProvider(cmd, args, cli.ProviderRunConfig{
		Name:         "gemini",
		Flags:        &geminiFlags,
		PromptFlag:   geminiPromptFlag,
		AllowedHosts: geminiAllowedHosts,
		WtFlag:       geminiWtFlag,
		GetCredentialGrant: func() string {
			if HasCredential() {
				return "gemini"
			}
			return ""
		},
		Dependencies:          DefaultDependencies(),
		NetworkHosts:          NetworkHosts(),
		SupportsInitialPrompt: false,
		DryRunNote:            "Note: No API key configured. Gemini will prompt for login.",
		BuildCommand: func(promptFlag, _ string) ([]string, error) {
			if promptFlag != "" {
				return []string{"gemini", "-p", promptFlag}, nil
			}
			return []string{"gemini"}, nil
		},
		ConfigureAgent: func(cfg *config.Config) {
			syncLogs := true
			cfg.Gemini.SyncLogs = &syncLogs
		},
	})
}
