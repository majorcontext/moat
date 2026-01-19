package cli

import (
	"github.com/andybons/moat/internal/log"
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
	},
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "show what would happen without executing")
	rootCmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "output in JSON format")
}
