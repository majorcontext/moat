package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/log"
	"github.com/spf13/cobra"
)

// ProviderRunConfig describes a provider's run configuration.
// Each provider supplies a ProviderRunConfig to RunProvider
// to eliminate repeated boilerplate for workspace resolution,
// grant dedup, config setup, dry run, and execution.
type ProviderRunConfig struct {
	// Name is the provider name (e.g., "claude", "codex", "gemini").
	// Used in log messages and dry run output.
	Name string

	// Flags is a pointer to the provider's ExecFlags.
	Flags *ExecFlags

	// PromptFlag is the value of the -p/--prompt flag.
	PromptFlag string

	// AllowedHosts are additional hosts from the --allow-host flag.
	AllowedHosts []string

	// WtFlag is the value of the --worktree flag.
	WtFlag string

	// GetCredentialGrant returns the grant name for the provider's credential.
	// Returns empty string if no credential exists.
	GetCredentialGrant func() string

	// Dependencies are the required dependencies (e.g., ["node@20", "git", "claude-code"]).
	Dependencies []string

	// NetworkHosts are hosts the provider needs network access to.
	NetworkHosts []string

	// BuildCommand builds the container command from the prompt flag value and
	// initial prompt (from -- args). Called after grants and interactive mode
	// are resolved.
	BuildCommand func(promptFlag, initialPrompt string) ([]string, error)

	// ConfigureAgent applies provider-specific config tweaks (e.g., syncLogs).
	// Called after dependencies and network hosts are added.
	// cfg is guaranteed non-nil.
	ConfigureAgent func(cfg *config.Config)

	// SupportsInitialPrompt indicates whether this provider supports the
	// -- "prompt" syntax for passing an initial prompt via cobra's ArgsLenAtDash.
	// If false, args are treated as a simple workspace path.
	SupportsInitialPrompt bool

	// DryRunNote is an optional extra line to print during dry run
	// (e.g., "Note: No API key configured. Claude will prompt for login.").
	// Only printed when grants is empty.
	DryRunNote string
}

// RunProvider executes the shared boilerplate for provider CLI commands.
// It handles workspace resolution, config loading, worktree support,
// grant dedup, dependency injection, network hosts, dry run, and execution.
func RunProvider(cmd *cobra.Command, args []string, rc ProviderRunConfig) error {
	// Guard: if a subcommand was invoked, skip the parent run function
	if cmd.CalledAs() != rc.Name {
		return nil
	}

	// Parse workspace and optional initial prompt from args
	workspace := "."
	var initialPrompt string

	if rc.SupportsInitialPrompt {
		dashIdx := cmd.ArgsLenAtDash()
		if dashIdx >= 0 {
			if dashIdx > 0 {
				workspace = args[0]
			}
			passthroughArgs := args[dashIdx:]
			if len(passthroughArgs) > 0 {
				initialPrompt = strings.Join(passthroughArgs, " ")
			}
		} else if len(args) > 0 {
			workspace = args[0]
		}
	} else {
		if len(args) > 0 {
			workspace = args[0]
		}
	}

	absPath, err := ResolveWorkspacePath(workspace)
	if err != nil {
		return err
	}

	cfg, err := config.Load(absPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Handle --wt/--worktree flag
	wtOut, err := ResolveWorktreeWorkspace(rc.WtFlag, absPath, rc.Flags, cfg)
	if err != nil {
		return err
	}
	absPath = wtOut.Workspace
	cfg = wtOut.Config

	// Build grants list with deduplication: credential grant first,
	// then config grants, then flag grants.
	grantSet := make(map[string]bool)
	var grants []string
	addGrant := func(g string) {
		if !grantSet[g] {
			grantSet[g] = true
			grants = append(grants, g)
		}
	}

	if rc.GetCredentialGrant != nil {
		if credName := rc.GetCredentialGrant(); credName != "" {
			addGrant(credName)
		}
	}
	if cfg != nil {
		for _, g := range cfg.Grants {
			addGrant(g)
		}
	}
	for _, g := range rc.Flags.Grants {
		addGrant(g)
	}
	rc.Flags.Grants = grants

	interactive := rc.PromptFlag == ""

	// Build container command (provider-specific logic)
	containerCmd, err := rc.BuildCommand(rc.PromptFlag, initialPrompt)
	if err != nil {
		return err
	}

	// Name from flag, or config, or let manager generate one
	if rc.Flags.Name == "" && cfg != nil && cfg.Name != "" {
		rc.Flags.Name = cfg.Name
	}

	// Ensure config is non-nil before modifying dependencies/network
	if cfg == nil {
		cfg = &config.Config{}
	}

	// Add required dependencies, skipping any already present
	for _, dep := range rc.Dependencies {
		prefix := dep
		for i := range dep {
			if dep[i] == '@' {
				prefix = dep[:i]
				break
			}
		}
		if !HasDependency(cfg.Dependencies, prefix) {
			cfg.Dependencies = append(cfg.Dependencies, dep)
		}
	}

	// Network: provider hosts first, then user-specified allowed hosts
	cfg.Network.Allow = append(cfg.Network.Allow, rc.NetworkHosts...)
	cfg.Network.Allow = append(cfg.Network.Allow, rc.AllowedHosts...)

	// Provider-specific config tweaks (e.g., enabling log sync)
	if rc.ConfigureAgent != nil {
		rc.ConfigureAgent(cfg)
	}

	if envErr := ParseEnvFlags(rc.Flags.Env, cfg); envErr != nil {
		return envErr
	}

	log.Debug(fmt.Sprintf("starting %s", rc.Name),
		"workspace", absPath,
		"grants", grants,
		"interactive", interactive,
		"prompt", rc.PromptFlag,
		"rebuild", rc.Flags.Rebuild,
	)

	if DryRun {
		fmt.Printf("Dry run - would start %s\n", rc.Name)
		fmt.Printf("Workspace: %s\n", absPath)
		fmt.Printf("Grants: %v\n", grants)
		fmt.Printf("Interactive: %v\n", interactive)
		fmt.Printf("Rebuild: %v\n", rc.Flags.Rebuild)
		if len(grants) == 0 && rc.DryRunNote != "" {
			fmt.Println(rc.DryRunNote)
		}
		return nil
	}

	ctx := context.Background()

	opts := ExecOptions{
		Flags:       *rc.Flags,
		Workspace:   absPath,
		Command:     containerCmd,
		Config:      cfg,
		Interactive: interactive,
	}

	SetWorktreeFields(&opts, wtOut.Result)

	result, err := ExecuteRun(ctx, opts)
	if err != nil {
		return err
	}

	if result != nil {
		fmt.Printf("Starting %s in %s\n", rc.Name, absPath)
		fmt.Printf("Run: %s (%s)\n", result.Name, result.ID)
	}

	return nil
}
