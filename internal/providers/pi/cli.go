package pi

import (
	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/ui"
)

var (
	piFlags        cli.ExecFlags
	piPromptFlag   string
	piAllowedHosts []string
	piWtFlag       string
	piProviderFlag string
	piModelFlag    string
)

// Resolved by runPi (before RunProvider's closures fire) so GetCredentialGrant
// and BuildCommand can see the chosen backend and model.
var (
	piResolvedProvider string
	piResolvedModel    string
)

// NetworkHosts lists the LLM API hosts Pi may need under a strict network
// policy. Every backend is allowed so a run works regardless of the resolved
// provider.
func NetworkHosts() []string {
	return []string{
		"api.anthropic.com",
		"api.openai.com",
		"gw.lunaroute.com",
		"mcp.lunaroute.com",
	}
}

// DefaultDependencies returns the default dependencies for running Pi.
func DefaultDependencies() []string {
	return []string{"node@22", "git", "pi-cli"}
}

// RegisterCLI registers the `moat pi` command.
func (p *Provider) RegisterCLI(root *cobra.Command) {
	piCmd := &cobra.Command{
		Use:   "pi [workspace] [flags]",
		Short: "Run the Pi coding agent in an isolated container",
		Long: `Run the Pi coding agent in an isolated container with automatic credential injection.

Pi has no credential of its own — it runs against your anthropic, openai, or
lunaroute grant. If exactly one of those grants is configured it is used
automatically; if more than one is, set pi.provider (or --provider) to choose.
The lunaroute backend installs LunaRoute's Pi extension into the image.

Your workspace is mounted at /workspace inside the container. API credentials are
injected transparently via the Moat proxy - Pi never sees raw tokens.

Examples:
  # Start Pi in the current directory (interactive)
  moat pi

  # Start Pi in a specific project
  moat pi ./my-project

  # Ask Pi to do something specific (non-interactive)
  moat pi -p "explain this codebase"

  # Force the OpenAI backend (when several grants are configured)
  moat pi --provider openai

  # Use LunaRoute (after 'moat grant lunaroute')
  moat pi --provider lunaroute

  # Add additional grants (e.g., for GitHub API access)
  moat pi --grant github

Use 'moat list' to see running and recent runs.`,
		Args: cobra.ArbitraryArgs,
		RunE: runPi,
	}

	cli.AddExecFlags(piCmd, &piFlags)
	piCmd.Flags().StringVarP(&piPromptFlag, "prompt", "p", "", "run with prompt (non-interactive mode)")
	piCmd.Flags().StringSliceVar(&piAllowedHosts, "allow-host", nil, "additional hosts to allow network access to")
	piCmd.Flags().StringVar(&piProviderFlag, "provider", "", "model backend: anthropic, openai, or lunaroute (overrides pi.provider)")
	piCmd.Flags().StringVar(&piModelFlag, "model", "", "model pattern to use (overrides pi.model)")
	piCmd.Flags().StringVar(&piWtFlag, "worktree", "", "run in a git worktree for this branch")
	piCmd.Flags().StringVar(&piWtFlag, "wt", "", "alias for --worktree")
	_ = piCmd.Flags().MarkHidden("wt")

	root.AddCommand(piCmd)
}

func runPi(cmd *cobra.Command, args []string) error {
	return cli.RunProvider(cmd, args, cli.ProviderRunConfig{
		Name:         "pi",
		Flags:        &piFlags,
		PromptFlag:   piPromptFlag,
		AllowedHosts: piAllowedHosts,
		WtFlag:       piWtFlag,
		// Resolve the backend against the final (post-worktree) config and fail
		// hard on a missing/ambiguous/unsupported grant before anything is
		// created. Runs before GetCredentialGrant/BuildCommand read the globals.
		Preflight:             resolvePiPreflight,
		GetCredentialGrant:    func() string { return piResolvedProvider },
		Dependencies:          DefaultDependencies(),
		NetworkHosts:          NetworkHosts(),
		SupportsInitialPrompt: true,
		BuildCommand: func(promptFlag, initialPrompt string) ([]string, error) {
			return buildPiCommand(promptFlag, initialPrompt), nil
		},
		ConfigureAgent: func(cfg *config.Config) {
			// Running `moat pi` means the pi agent, regardless of any `agent:`
			// field in moat.yaml (which only sets the `moat run` default). This
			// makes the isPiRun guard in Create reliable.
			cfg.Agent = "pi"

			// The lunaroute backend only has models once LunaRoute's
			// extension is installed; bake it rather than make every user
			// list it under pi.packages.
			if piResolvedProvider == backendLunaRoute {
				cfg.Pi.Packages = withLunaRouteExtension(cfg.Pi.Packages)
			}

			// Pi config (baseUrl/streamSimple/extensions) can redirect model
			// traffic to arbitrary hosts, so only the network policy actually
			// contains egress. Warn when it isn't strict (empty == permissive).
			if cfg.Network.Policy != "strict" {
				ui.Warn("Pi runs under a permissive network policy: Pi extensions/config can redirect " +
					"model traffic to arbitrary hosts. Use `network.policy: strict` for untrusted work.")
			}
		},
	})
}

// resolvePiPreflight resolves the Pi backend from --provider/--model flags, the
// final config's pi block, and the credential store, then stashes the result
// for GetCredentialGrant and buildPiCommand. It fails hard (before any resource
// is created) on a missing, ambiguous, or unsupported backend. cfg may be nil.
func resolvePiPreflight(cfg *config.Config) error {
	providerOverride := piProviderFlag
	modelOverride := piModelFlag
	if cfg != nil {
		if providerOverride == "" {
			providerOverride = cfg.Pi.Provider
		}
		if modelOverride == "" {
			modelOverride = cfg.Pi.Model
		}
	}

	anthropicCred := loadCredential(credential.ProviderAnthropic)
	prov, model, err := resolvePiProvider(providerOverride, modelOverride, piGrants{
		Anthropic: anthropicCred != nil,
		OpenAI:    loadCredential(credential.ProviderOpenAI) != nil,
		LunaRoute: loadCredential(credential.ProviderLunaRoute) != nil,
	})
	if err != nil {
		return err
	}
	if anthropicCred != nil {
		if err := checkAnthropicGatewayKey(prov, anthropicCred.Metadata[credential.MetaKeyBaseURL]); err != nil {
			return err
		}
	}
	piResolvedProvider = prov
	piResolvedModel = model
	return nil
}

// buildPiCommand assembles the container command for Pi. Extracted from the
// BuildCommand closure so it is unit-testable.
func buildPiCommand(promptFlag, initialPrompt string) []string {
	c := []string{"--provider", piResolvedProvider}
	if piResolvedModel != "" {
		c = append(c, "--model", piResolvedModel)
	}
	c = append(c, "--append-system-prompt", PiInitMountPath+"/"+ContextFileName)
	if promptFlag != "" {
		c = append(c, "-p", promptFlag)
	} else if initialPrompt != "" {
		c = append(c, initialPrompt)
	}
	if piResolvedProvider == backendLunaRoute {
		// $0 is "pi"; the args follow as "$@", so nothing is re-quoted.
		return append([]string{"sh", "-c", lunaRouteLaunchScript, "pi"}, c...)
	}
	return append([]string{"pi"}, c...)
}

// loadCredential returns the stored credential for prov, or nil if it is not
// configured (or the store can't be read).
func loadCredential(prov credential.Provider) *credential.Credential {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return nil
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return nil
	}
	cred, err := store.Get(prov)
	if err != nil {
		return nil
	}
	return cred
}
