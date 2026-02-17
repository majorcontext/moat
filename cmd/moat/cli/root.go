// Package cli implements the moat command-line interface using Cobra.
// It provides commands for managing AI agent runs, credentials, containers,
// and observability features.
package cli

import (
	"os"
	"path/filepath"

	intcli "github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/spf13/cobra"
)

var (
	verbose bool
	dryRun  bool
	jsonOut bool
	profile string
)

var rootCmd = &cobra.Command{
	Use:   "moat",
	Short: "Moat - Local execution infrastructure for AI agents",
	Long: `Moat is local execution infrastructure for AI agents.
The core abstraction is a run — a sealed, ephemeral workspace containing
code, dependencies, tools, credentials, and observability.

Core promise: moat run my-agent . just works — zero Docker knowledge,
zero secret copying, full visibility.`,
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Resolve profile: --profile flag > MOAT_PROFILE env var
		if profile == "" {
			profile = os.Getenv("MOAT_PROFILE")
		}
		if profile != "" {
			if err := credential.ValidateProfile(profile); err != nil {
				return err
			}
			credential.ActiveProfile = profile
		}

		// Load global config for debug settings
		globalCfg, _ := config.LoadGlobal()
		debugDir := filepath.Join(config.GlobalConfigDir(), "debug")

		// Check if this is an interactive command
		interactive := false
		if cmd.Flags().Lookup("interactive") != nil {
			interactive, _ = cmd.Flags().GetBool("interactive")
		}

		if err := log.Init(log.Options{
			Verbose:       verbose,
			JSONFormat:    jsonOut,
			Interactive:   interactive,
			DebugDir:      debugDir,
			RetentionDays: globalCfg.Debug.RetentionDays,
		}); err != nil {
			// Log init failure is non-fatal - fallback to default logger
			// Just print to stderr since logging may not be working
			cmd.PrintErrf("Warning: failed to initialize debug logging: %v\n", err)
		}

		// Sync dry-run state to internal/cli package for providers
		intcli.DryRun = dryRun
		return nil
	},
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

// RegisterProviderCLI registers CLI commands for all agent providers.
// This must be called after providers have registered themselves (e.g., after
// providers.RegisterAll() in main.go).
func RegisterProviderCLI() {
	for _, agent := range provider.Agents() {
		agent.RegisterCLI(rootCmd)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "show what would happen without executing")
	rootCmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "output in JSON format")
	rootCmd.PersistentFlags().StringVar(&profile, "profile", "", "credential profile to use (env: MOAT_PROFILE)")

	// Store root command for providers that may need it
	intcli.RootCmd = rootCmd
}
