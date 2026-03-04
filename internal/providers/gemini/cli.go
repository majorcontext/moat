package gemini

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
)

// QuickstartPromptBuilder builds the quickstart analysis prompt.
// Set by cmd/moat to break the import cycle (providers/gemini -> quickstart -> deps -> providers/gemini).
var QuickstartPromptBuilder func() string

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

	quickstartCmd := &cobra.Command{
		Use:   "quickstart [workspace]",
		Short: "Auto-generate moat.yaml for an existing project",
		Long: `Analyze the project and generate a moat.yaml configuration file.

Runs Gemini CLI in a bootstrap container to analyze your project's
manifest files, source code, and README, then generates an appropriate
moat.yaml configuration.

Requires a Google credential (run 'moat grant gemini' first).

Examples:
  moat gemini quickstart
  moat gemini quickstart /path/to/project`,
		Args: cobra.MaximumNArgs(1),
		RunE: runGeminiQuickstart,
	}
	geminiCmd.AddCommand(quickstartCmd)

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

func runGeminiQuickstart(cmd *cobra.Command, args []string) error {
	workspace := "."
	if len(args) > 0 {
		workspace = args[0]
	}

	absPath, err := cli.ResolveWorkspacePath(workspace)
	if err != nil {
		return err
	}

	// Check if moat.yaml already exists
	if _, err := os.Stat(filepath.Join(absPath, "moat.yaml")); err == nil {
		return fmt.Errorf("moat.yaml already exists in %s\n\nTo regenerate, remove the existing file first.", absPath)
	}

	if QuickstartPromptBuilder == nil {
		return fmt.Errorf("quickstart is not available (prompt builder not registered)")
	}
	prompt := QuickstartPromptBuilder() + "\nWrite the generated YAML directly to /workspace/moat.yaml.\n"

	// Use a fresh ExecFlags for quickstart (don't inherit from parent gemini command)
	var qsFlags cli.ExecFlags

	return cli.RunProvider(cmd, args, cli.ProviderRunConfig{
		Name:       "quickstart",
		Flags:      &qsFlags,
		PromptFlag: prompt,
		GetCredentialGrant: func() string {
			if HasCredential() {
				return "gemini"
			}
			return ""
		},
		Dependencies: DefaultDependencies(),
		NetworkHosts: NetworkHosts(),
		BuildCommand: func(promptFlag, initialPrompt string) ([]string, error) {
			return []string{"gemini", "-p", promptFlag}, nil
		},
		ConfigureAgent: func(cfg *config.Config) {
			// Ensure agent is "gemini" for proper image selection,
			// since Name is "quickstart" for the CalledAs() guard.
			cfg.Agent = "gemini"
		},
	})
}
