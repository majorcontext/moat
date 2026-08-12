package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/netrules"
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

	// Preflight, if set, runs after workspace/worktree/config resolution and
	// before grant assembly. It receives the final resolved config (which may
	// be nil when there is no moat.yaml) so a provider can validate and resolve
	// against the config that will actually be used — including a worktree's own
	// moat.yaml — and fail hard before anything is created. A returned error
	// aborts the run.
	Preflight func(cfg *config.Config) error

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

	// Preflight runs against the final resolved config (post-worktree), before
	// grants are assembled, so a provider can fail hard on bad configuration
	// before any resources are created.
	if rc.Preflight != nil {
		if pErr := rc.Preflight(cfg); pErr != nil {
			return pErr
		}
	}

	// Expand `agents:` into dependencies/grants/network hosts before grant
	// resolution and the network-rule loop below — an expansion that lands
	// after either contributes nothing: the grants never reach buildGrants
	// (which reads cfg.Grants immediately below) and the hosts never reach
	// the proxy registration (see ExpandAgents doc comment). cfg may still be
	// nil here (no moat.yaml); ExpandAgents is a no-op in that case since a
	// nil config can't carry an agents: list either.
	var derivedGrants []string
	derivedGrants, err = ExpandAgents(cfg)
	if err != nil {
		return err
	}

	// Build grants list with deduplication: credential grant first,
	// then config grants, then flag grants, then agents:-derived grants.
	// Auto-detected grants are suppressed when they conflict with an
	// explicit grant.
	var autoDetected string
	if rc.GetCredentialGrant != nil {
		autoDetected = rc.GetCredentialGrant()
	}
	var configGrants []string
	if cfg != nil {
		configGrants = cfg.Grants
	}
	grants := buildGrants(autoDetected, configGrants, rc.Flags.Grants, derivedGrants)
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

	// Network: provider hosts first, then user-specified allowed hosts.
	// Convert plain host strings to NetworkRuleEntry (host-level allow, no method/path rules).
	for _, host := range append(rc.NetworkHosts, rc.AllowedHosts...) {
		cfg.Network.Rules = append(cfg.Network.Rules, netrules.NetworkRuleEntry{HostRules: netrules.HostRules{Host: host}})
	}

	// Snapshot cfg.Agent before provider-specific hooks run. Some ConfigureAgent
	// hooks (copilot, pi, init) unconditionally overwrite cfg.Agent with their
	// own name below, which would otherwise erase the moat.yaml value before
	// the conflict check further down ever sees it.
	agentBeforeConfigure := cfg.Agent

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

	// The verb the user typed always names the agent. ValidateAgent runs inside
	// so an unknown moat.yaml value warns once and is discarded.
	resolveProviderAgentField(rc.Name, cfg, agentBeforeConfigure)

	opts := ExecOptions{
		Flags:       *rc.Flags,
		Workspace:   absPath,
		Command:     containerCmd,
		Config:      cfg,
		Interactive: interactive,
	}

	SetWorktreeFields(&opts, wtOut.Result)

	// Print run info after creation but before blocking on execution
	opts.OnRunCreated = func(info RunInfo) {
		fmt.Printf("Started agent %q (%s)\n", info.Name, info.ID)
	}

	_, err = ExecuteRun(ctx, opts)
	return err
}

// agentVerbFor selects the verb passed to ResolveAgentField for a given
// provider run. "init" is not an agent name — moat init's own ConfigureAgent
// hook (which runs earlier, at line ~186) already sets cfg.Agent to the
// auto-detected agent, so that value is reused as the verb instead of
// overwriting it with the literal string "init".
func agentVerbFor(rcName string, cfg *config.Config) string {
	if rcName == "init" {
		return cfg.Agent
	}
	return rcName
}

// resolveProviderAgentField determines and applies the final cfg.Agent for a
// provider run. preConfigureAgent is the cfg.Agent snapshot taken before
// rc.ConfigureAgent ran: several hooks (copilot, pi, init) unconditionally
// overwrite cfg.Agent with their own provider name, which — if left in
// place — would make ResolveAgentField's conflict check compare the provider
// name against itself and silently swallow the warning. Restoring the
// snapshot first means the check always compares against what moat.yaml
// actually said, uniformly across every provider command.
//
// agentVerbFor still needs the post-ConfigureAgent value for "init" (it reads
// cfg.Agent to find the auto-detected agent), so the verb is computed before
// the snapshot is restored.
func resolveProviderAgentField(rcName string, cfg *config.Config, preConfigureAgent string) {
	verb := agentVerbFor(rcName, cfg)
	cfg.Agent = preConfigureAgent
	ResolveAgentField(cfg, verb)
}

// containsGrant reports whether grants contains the named grant.
func containsGrant(grants []string, name string) bool {
	for _, g := range grants {
		if g == name {
			return true
		}
	}
	return false
}

// grantsEquivalent reports whether a and b name the same underlying
// credential under different grant keys — today, only the claude
// (OAuth-token) / anthropic (API-key) pair, which both authenticate Claude
// Code against the same host.
func grantsEquivalent(a, b string) bool {
	return (a == "claude" && b == "anthropic") || (a == "anthropic" && b == "claude")
}

// buildGrants assembles the final grants list from an auto-detected
// credential grant, config grants, flag grants, and agents:-derived grants,
// in descending precedence.
//
// Auto-detected grants are suppressed when they conflict with an EXPLICIT
// (config or flag) grant — e.g. "claude" conflicts with "anthropic" since
// both target the same host. derivedGrants (from moat.yaml's `agents:`
// expansion, see ExpandAgents) never participates in that suppression: it is
// a machine-filled fallback, not a user declaration, so it must not outrank
// whatever credential the user actually has. A derived grant is instead
// dropped when an equivalent credential is already present in the result —
// otherwise a user who stores their Anthropic credential as an API key
// (autoDetected == "anthropic") would have it discarded in favor of a
// "claude" grant injected by `agents: [claude, ...]`, forcing an OAuth login
// they never asked for even though their existing credential already works.
func buildGrants(autoDetected string, configGrants, flagGrants, derivedGrants []string) []string {
	grantSet := make(map[string]bool)
	var grants []string
	addGrant := func(g string) {
		if !grantSet[g] {
			grantSet[g] = true
			grants = append(grants, g)
		}
	}
	equivalentPresent := func(g string) bool {
		for _, existing := range grants {
			if grantsEquivalent(existing, g) {
				return true
			}
		}
		return false
	}

	explicitGrants := make([]string, 0, len(configGrants)+len(flagGrants))
	explicitGrants = append(explicitGrants, configGrants...)
	explicitGrants = append(explicitGrants, flagGrants...)

	if autoDetected != "" {
		suppressed := (autoDetected == "claude" && containsGrant(explicitGrants, "anthropic")) ||
			(autoDetected == "anthropic" && containsGrant(explicitGrants, "claude"))
		if !suppressed {
			addGrant(autoDetected)
		}
	}
	for _, g := range explicitGrants {
		addGrant(g)
	}
	for _, g := range derivedGrants {
		if equivalentPresent(g) {
			continue
		}
		addGrant(g)
	}
	return grants
}
