package kiro

import (
	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
)

var (
	kiroFlags        cli.ExecFlags
	kiroPromptFlag   string
	kiroAllowedHosts []string
	kiroWtFlag       string
)

// NetworkHosts returns the hosts Kiro needs network access to.
func NetworkHosts() []string {
	hosts := make([]string, 0, 1+len(kiroAPIHosts)+len(kiroPassthroughHosts))
	hosts = append(hosts, "cli.kiro.dev")
	hosts = append(hosts, kiroAPIHosts...)
	hosts = append(hosts, kiroPassthroughHosts...)
	return hosts
}

// DefaultDependencies returns the default dependencies for running Kiro CLI.
func DefaultDependencies() []string {
	return []string{"kiro-cli", "git"}
}

// RegisterCLI registers the `moat kiro` command. Called automatically for
// every AgentProvider by cmd/moat/cli/root.go.
func (p *Provider) RegisterCLI(root *cobra.Command) {
	kiroCmd := &cobra.Command{
		Use:   "kiro [workspace] [flags]",
		Short: "Run Kiro CLI in an isolated container",
		Long: `Run the Kiro CLI in an isolated container with automatic credential injection.

Your workspace is mounted at /workspace inside the container. Kiro credentials
are injected transparently via the Moat proxy - Kiro never sees raw tokens.

Examples:
  # Start Kiro in the current directory (interactive)
  moat kiro

  # Start Kiro in a specific project
  moat kiro ./my-project

  # Ask Kiro to do something specific (non-interactive)
  moat kiro -p "explain this codebase"

  # Add additional grants (e.g., for GitHub API access)
  moat kiro --grant github

Use 'moat list' to see running and recent runs.`,
		Args: cobra.ArbitraryArgs,
		RunE: runKiro,
	}

	cli.AddExecFlags(kiroCmd, &kiroFlags)
	kiroCmd.Flags().StringVarP(&kiroPromptFlag, "prompt", "p", "", "run with prompt (non-interactive mode)")
	kiroCmd.Flags().StringSliceVar(&kiroAllowedHosts, "allow-host", nil, "additional hosts to allow network access to")
	kiroCmd.Flags().StringVar(&kiroWtFlag, "worktree", "", "run in a git worktree for this branch")
	kiroCmd.Flags().StringVar(&kiroWtFlag, "wt", "", "alias for --worktree")
	_ = kiroCmd.Flags().MarkHidden("wt")

	root.AddCommand(kiroCmd)
}

func runKiro(cmd *cobra.Command, args []string) error {
	return cli.RunProvider(cmd, args, cli.ProviderRunConfig{
		Name:                  "kiro",
		Flags:                 &kiroFlags,
		PromptFlag:            kiroPromptFlag,
		AllowedHosts:          kiroAllowedHosts,
		WtFlag:                kiroWtFlag,
		GetCredentialGrant:    GetCredentialName,
		Dependencies:          DefaultDependencies(),
		NetworkHosts:          NetworkHosts(),
		SupportsInitialPrompt: true,
		DryRunNote:            "Note: No Kiro token configured. Run 'moat grant kiro'.",
		BuildCommand: func(promptFlag, initialPrompt string) ([]string, error) {
			containerCmd := []string{"kiro-cli", "chat", "--trust-all-tools", "--trust-tools=execute_bash"}
			if promptFlag != "" {
				return append(containerCmd, "--no-interactive", promptFlag), nil
			}
			if initialPrompt != "" {
				containerCmd = append(containerCmd, initialPrompt)
			}
			return containerCmd, nil
		},
		ConfigureAgent: func(cfg *config.Config) {
			syncLogs := true
			cfg.Kiro.SyncLogs = &syncLogs
		},
	})
}

// GetCredentialName returns "kiro" if a Kiro credential exists, else "".
func GetCredentialName() string {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return ""
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return ""
	}
	if _, err := store.Get(credential.ProviderKiro); err == nil {
		return "kiro"
	}
	return ""
}
