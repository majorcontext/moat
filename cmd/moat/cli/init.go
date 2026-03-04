package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	intcli "github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	claudeprov "github.com/majorcontext/moat/internal/providers/claude"
	codexprov "github.com/majorcontext/moat/internal/providers/codex"
	geminiprov "github.com/majorcontext/moat/internal/providers/gemini"
	"github.com/majorcontext/moat/internal/quickstart"
	"github.com/majorcontext/moat/internal/ui"
)

// agentConfig holds provider-specific configuration for running quickstart.
type agentConfig struct {
	name         string
	dependencies []string
	networkHosts []string
	getCredGrant func() string
	buildCommand func(prompt, initial string) ([]string, error)
}

// agentConfigs returns the supported agents in preference order.
func agentConfigs() []agentConfig {
	return []agentConfig{
		{
			name:         "claude",
			dependencies: claudeprov.DefaultDependencies(),
			networkHosts: claudeprov.NetworkHosts(),
			getCredGrant: claudeprov.GetCredentialName,
			buildCommand: func(prompt, _ string) ([]string, error) {
				return []string{"claude", "--dangerously-skip-permissions", "-p", prompt}, nil
			},
		},
		{
			name:         "codex",
			dependencies: codexprov.DefaultDependencies(),
			networkHosts: codexprov.NetworkHosts(),
			getCredGrant: codexprov.GetCredentialName,
			buildCommand: func(prompt, _ string) ([]string, error) {
				return []string{"codex", "exec", "--full-auto", prompt}, nil
			},
		},
		{
			name:         "gemini",
			dependencies: geminiprov.DefaultDependencies(),
			networkHosts: geminiprov.NetworkHosts(),
			getCredGrant: func() string {
				if geminiprov.HasCredential() {
					return "gemini"
				}
				return ""
			},
			buildCommand: func(prompt, _ string) ([]string, error) {
				return []string{"gemini", "-p", prompt}, nil
			},
		},
	}
}

var initCmd = &cobra.Command{
	Use:   "init [workspace]",
	Short: "Auto-generate moat.yaml for an existing project",
	Long: `Analyze the project and generate a moat.yaml configuration file.

Runs an AI agent in a bootstrap container to analyze your project's
manifest files, source code, and README, then generates an appropriate
moat.yaml configuration.

Automatically detects which agent credentials are available (Claude,
Codex, or Gemini). If no credentials exist, prompts you to grant one.

Examples:
  moat init
  moat init /path/to/project`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	workspace := "."
	if len(args) > 0 {
		workspace = args[0]
	}

	absPath, err := intcli.ResolveWorkspacePath(workspace)
	if err != nil {
		return err
	}

	// Check if moat.yaml already exists.
	if _, err := os.Stat(filepath.Join(absPath, "moat.yaml")); err == nil {
		return fmt.Errorf("moat.yaml already exists in %s\n\nTo regenerate, remove the existing file first.", absPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking for existing moat.yaml: %w", err)
	}

	// Auto-detect which agent has credentials.
	var agent *agentConfig
	for _, ac := range agentConfigs() {
		if grant := ac.getCredGrant(); grant != "" {
			agent = &ac
			break
		}
	}

	if agent == nil {
		return fmt.Errorf("no AI agent credentials found\n\nRun one of the following to grant credentials:\n  moat grant claude     (Claude Code)\n  moat grant codex      (OpenAI Codex)\n  moat grant gemini     (Google Gemini)\n\nThen run 'moat init' again.")
	}

	prompt := quickstart.BuildPrompt(absPath) + "\nWrite the generated YAML directly to /workspace/moat.yaml.\n"

	ui.Info("Analyzing project to generate moat.yaml...")
	ui.Infof("Workspace: %s", absPath)
	ui.Infof("Using agent: %s", agent.name)

	var qsFlags intcli.ExecFlags

	return intcli.RunProvider(cmd, args, intcli.ProviderRunConfig{
		Name:               "init",
		Flags:              &qsFlags,
		PromptFlag:         prompt,
		GetCredentialGrant: agent.getCredGrant,
		Dependencies:       agent.dependencies,
		NetworkHosts:       agent.networkHosts,
		BuildCommand:       agent.buildCommand,
		ConfigureAgent: func(cfg *config.Config) {
			cfg.Agent = agent.name
		},
	})
}
