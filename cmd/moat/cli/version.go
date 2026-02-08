package cli

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Build-time variables injected via ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Version returns the build version string.
func Version() string {
	return version
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version of moat",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("moat %s\n", version)
		if commit != "none" {
			fmt.Printf("  commit: %s\n", commit)
		}
		if date != "unknown" {
			fmt.Printf("  built:  %s\n", date)
		}
		if info, ok := debug.ReadBuildInfo(); ok {
			fmt.Printf("  go:     %s\n", info.GoVersion)
		}
	},
}
