package copilot

import (
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
	copilotContextFlag          string
	copilotReasoningEffortFlag  string
	copilotExperimental         bool
	copilotAutopilot            bool
	copilotResolvedModel        string
	copilotResolvedContext      string
	copilotResolvedEffort       string
	copilotCredentialConfigured = defaultCopilotCredentialConfigured
)

func NetworkHosts() []string {
	return []string{
		copilotAPIHost,
		copilotGitHost,
		copilotProxyHost,
		copilotChatAPIHost,
		copilotBusinessHost,
		copilotMCPHost,
		copilotTelemetry,
	}
}

func DefaultDependencies() []string {
	return []string{"node@22", "git", "gh", "copilot-cli"}
}

func (p *Provider) RegisterCLI(root *cobra.Command) {
	copilotCmd := &cobra.Command{
		Use:   "copilot [workspace] [flags] [-- initial-prompt]",
		Short: "Run GitHub Copilot CLI in an isolated container",
		Long: `Run GitHub Copilot CLI in an isolated container with automatic credential injection.

Your workspace is mounted at /workspace inside the container. GitHub credentials
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
	copilotCmd.Flags().StringVar(&copilotContextFlag, "context", "", "context window tier: default or long_context (overrides copilot.context)")
	copilotCmd.Flags().StringVar(&copilotReasoningEffortFlag, "reasoning-effort", "", "reasoning effort level (overrides copilot.reasoning_effort)")
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
		DryRunNote:            "Note: No GitHub credential configured. Run 'moat grant github' first.",
		BuildCommand: func(promptFlag, initialPrompt string) ([]string, error) {
			return buildCopilotCommand(promptFlag, initialPrompt), nil
		},
		ConfigureAgent: configureCopilotAgent,
	})
}

// configureCopilotAgent writes the flag-resolved copilot values back into the
// config so downstream consumers (e.g. the run manager populating
// provider.PrepareOpts) see the same precedence the CLI applied: flag over
// moat.yaml. RunProvider calls this after guaranteeing cfg is non-nil, unlike
// Preflight which may receive nil when no moat.yaml exists.
func configureCopilotAgent(cfg *config.Config) {
	cfg.Agent = copilotProviderName
	cfg.Copilot.Model = copilotResolvedModel
	cfg.Copilot.Context = copilotResolvedContext
	cfg.Copilot.ReasoningEffort = copilotResolvedEffort
}

func resolveCopilotPreflight(cfg *config.Config) error {
	copilotResolvedModel = copilotModelFlag
	copilotResolvedContext = copilotContextFlag
	copilotResolvedEffort = copilotReasoningEffortFlag
	if cfg != nil {
		cfg.Grants = normalizeCopilotGrants(cfg.Grants, false)
		if copilotResolvedModel == "" {
			copilotResolvedModel = cfg.Copilot.Model
		}
		if copilotResolvedContext == "" {
			copilotResolvedContext = cfg.Copilot.Context
		}
		if copilotResolvedEffort == "" {
			copilotResolvedEffort = cfg.Copilot.ReasoningEffort
		}
		if cfg.Copilot.Experimental {
			copilotExperimental = true
		}
		if cfg.Copilot.Autopilot {
			copilotAutopilot = true
		}
	}
	copilotFlags.Grants = normalizeCopilotGrants(copilotFlags.Grants, true)
	// When no stored GitHub credential exists and github isn't already in the
	// grant list, add it so validateGrants triggers the inline grant prompt.
	// When the credential IS configured, GetCredentialGrant (called by
	// RunProvider's buildGrants) returns "github" and handles insertion there.
	if !cli.DryRun && !copilotCredentialConfigured() &&
		!hasBaseGrant(copilotFlags.Grants, "github") &&
		(cfg == nil || !hasBaseGrant(cfg.Grants, "github")) {
		copilotFlags.Grants = append(copilotFlags.Grants, "github")
	}
	return nil
}

func normalizeCopilotGrants(grants []string, warn bool) []string {
	if len(grants) == 0 {
		return grants
	}
	out := make([]string, 0, len(grants))
	replaced := false
	hasGitHub := false
	for _, grant := range grants {
		switch strings.Split(grant, ":")[0] {
		case copilotProviderName:
			replaced = true
			if !hasGitHub {
				out = append(out, "github")
				hasGitHub = true
			}
			continue
		case "github":
			if hasGitHub {
				continue
			}
			hasGitHub = true
		}
		out = append(out, grant)
	}
	if replaced && warn {
		ui.Warn("using github grant for moat copilot — Copilot uses the existing GitHub credential")
	}
	return out
}

func hasBaseGrant(grants []string, name string) bool {
	for _, grant := range grants {
		if strings.Split(grant, ":")[0] == name {
			return true
		}
	}
	return false
}

func buildCopilotCommand(promptFlag, initialPrompt string) []string {
	cmd := []string{"copilot", "--no-auto-update"}
	if copilotResolvedModel != "" {
		cmd = append(cmd, "--model", copilotResolvedModel)
	}
	if copilotResolvedContext != "" {
		cmd = append(cmd, "--context", copilotResolvedContext)
	}
	if copilotResolvedEffort != "" {
		cmd = append(cmd, "--reasoning-effort", copilotResolvedEffort)
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
		return string(credential.ProviderGitHub)
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
	if _, err := store.Get(credential.ProviderGitHub); err == nil {
		return true
	}
	return false
}
