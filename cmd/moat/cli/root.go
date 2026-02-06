// Package cli implements the moat command-line interface using Cobra.
// It provides commands for managing AI agent runs, credentials, containers,
// and observability features.
package cli

import (
	intcli "github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/spf13/cobra"
)

var (
	verbose bool
	dryRun  bool
	jsonOut bool
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
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		log.Init(verbose, jsonOut)
		// Sync dry-run state to internal/cli package for providers
		intcli.DryRun = dryRun
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

	// Store root command for providers that may need it
	intcli.RootCmd = rootCmd
}
