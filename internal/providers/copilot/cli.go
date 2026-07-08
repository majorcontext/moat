package copilot

import (
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/ui"
)

var (
	copilotFlags                cli.ExecFlags
	copilotPromptFlag           string
	copilotAllowedHosts         []string
	copilotWtFlag               string
	copilotAllowAll             bool
	copilotModelFlag            string
	copilotExperimental         bool
	copilotAutopilot            bool
	copilotResolvedModel        string
	copilotCredentialConfigured = defaultCopilotCredentialConfigured
)

func NetworkHosts() []string {
	return []string{
		"api.github.com",
		"github.com",
		"copilot-proxy.githubusercontent.com",
		"api.githubcopilot.com",
		"api.business.githubcopilot.com",
		"api.mcp.github.com",
		"telemetry.business.githubcopilot.com",
	}
}

func DefaultDependencies() []string {
	return []string{"node@22", "git", "gh", "copilot-cli"}
}

func (p *Provider) RegisterCLI(root *cobra.Command) {
	copilotCmd := &cobra.Command{
		Use:   "copilot [workspace] [flags]",
		Short: "Run GitHub Copilot CLI in an isolated container",
		Long: `Run GitHub Copilot CLI in an isolated container with automatic credential injection.

Your workspace is mounted at /workspace inside the container. Copilot credentials
are injected transparently via the Moat proxy - Copilot CLI never sees the raw
token stored by Moat.

Examples:
  moat copilot
  moat copilot ./my-project
  moat copilot -p "explain this codebase"
  moat copilot -p "fix the failing tests" --allow-all
  moat copilot --model gpt-5.4`,
		Args: cobra.ArbitraryArgs,
		RunE: runCopilot,
	}

	cli.AddExecFlags(copilotCmd, &copilotFlags)
	copilotCmd.Flags().StringVarP(&copilotPromptFlag, "prompt", "p", "", "run with prompt (non-interactive mode)")
	copilotCmd.Flags().StringSliceVar(&copilotAllowedHosts, "allow-host", nil, "additional hosts to allow network access to")
	copilotCmd.Flags().BoolVar(&copilotAllowAll, "allow-all", true, "allow all Copilot tools, paths, and URLs without prompting")
	copilotCmd.Flags().StringVar(&copilotModelFlag, "model", "", "model to use (overrides copilot.model)")
	copilotCmd.Flags().BoolVar(&copilotExperimental, "experimental", false, "enable Copilot CLI experimental features")
	copilotCmd.Flags().BoolVar(&copilotAutopilot, "autopilot", false, "start Copilot CLI in autopilot mode")
	copilotCmd.Flags().StringVar(&copilotWtFlag, "worktree", "", "run in a git worktree for this branch")
	copilotCmd.Flags().StringVar(&copilotWtFlag, "wt", "", "alias for --worktree")
	_ = copilotCmd.Flags().MarkHidden("wt")

	root.AddCommand(copilotCmd)
}

func runCopilot(cmd *cobra.Command, args []string) error {
	return cli.RunProvider(cmd, args, cli.ProviderRunConfig{
		Name:                  copilotProviderName,
		Flags:                 &copilotFlags,
		PromptFlag:            copilotPromptFlag,
		AllowedHosts:          copilotAllowedHosts,
		WtFlag:                copilotWtFlag,
		Preflight:             resolveCopilotPreflight,
		GetCredentialGrant:    GetCredentialName,
		Dependencies:          DefaultDependencies(),
		NetworkHosts:          NetworkHosts(),
		SupportsInitialPrompt: true,
		DryRunNote:            "Note: No Copilot credential configured. Run 'moat grant copilot' first.",
		BuildCommand: func(promptFlag, initialPrompt string) ([]string, error) {
			return buildCopilotCommand(promptFlag, initialPrompt), nil
		},
		ConfigureAgent: func(cfg *config.Config) {
			cfg.Agent = copilotProviderName
		},
	})
}

func resolveCopilotPreflight(cfg *config.Config) error {
	copilotResolvedModel = copilotModelFlag
	if cfg != nil {
		cfg.Grants = filterGitHubGrant(cfg.Grants, false)
		if copilotResolvedModel == "" {
			copilotResolvedModel = cfg.Copilot.Model
		}
		if cfg.Copilot.Experimental {
			copilotExperimental = true
		}
		if cfg.Copilot.Autopilot {
			copilotAutopilot = true
		}
	}
	copilotFlags.Grants = filterGitHubGrant(copilotFlags.Grants, true)
	if !cli.DryRun && !copilotCredentialConfigured() && !slices.Contains(copilotFlags.Grants, copilotProviderName) {
		copilotFlags.Grants = append(copilotFlags.Grants, copilotProviderName)
	}
	return nil
}

func filterGitHubGrant(grants []string, warn bool) []string {
	if len(grants) == 0 {
		return grants
	}
	out := grants[:0]
	removed := false
	for _, grant := range grants {
		if strings.Split(grant, ":")[0] == "github" {
			removed = true
			continue
		}
		out = append(out, grant)
	}
	if removed && warn {
		ui.Warn("ignoring github grant for moat copilot — the copilot grant already injects GitHub API and HTTPS git auth for this run")
	}
	return out
}

func buildCopilotCommand(promptFlag, initialPrompt string) []string {
	cmd := []string{"copilot", "--no-auto-update"}
	if copilotResolvedModel != "" {
		cmd = append(cmd, "--model", copilotResolvedModel)
	}
	if copilotExperimental {
		cmd = append(cmd, "--experimental")
	}
	if copilotAutopilot {
		cmd = append(cmd, "--autopilot")
	}
	if copilotAllowAll {
		cmd = append(cmd, "--allow-all")
	}
	if promptFlag != "" {
		cmd = append(cmd, "-p", promptFlag)
	} else if initialPrompt != "" {
		cmd = append(cmd, "-i", initialPrompt)
	}
	return cmd
}

func GetCredentialName() string {
	if copilotCredentialConfigured() {
		return string(credential.ProviderCopilot)
	}
	return ""
}

func defaultCopilotCredentialConfigured() bool {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return false
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return false
	}
	if _, err := store.Get(credential.ProviderCopilot); err == nil {
		return true
	}
	return false
}
