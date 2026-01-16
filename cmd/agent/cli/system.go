package cli

import (
	"github.com/spf13/cobra"
)

var systemCmd = &cobra.Command{
	Use:   "system",
	Short: "Low-level container system commands",
	Long: `Access underlying container system resources.

These commands are escape hatches for debugging and advanced use.
For normal operations, use 'agent status' and 'agent clean' instead.`,
}

func init() {
	rootCmd.AddCommand(systemCmd)
}
