package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/andybons/agentops/internal/deps"
	"github.com/spf13/cobra"
)

var depsCmd = &cobra.Command{
	Use:   "deps",
	Short: "Manage dependencies",
	Long:  `List and inspect available dependencies for agent runs.`,
}

var depsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available dependencies",
	Long: `List all dependencies that can be used in agent.yaml.

Examples:
  agent deps list
  agent deps list --type runtime
  agent deps list --type npm`,
	RunE: runDepsList,
}

var depsInfoCmd = &cobra.Command{
	Use:   "info [name]",
	Short: "Show dependency details",
	Long: `Show detailed information about a specific dependency.

Examples:
  agent deps info node
  agent deps info playwright`,
	Args: cobra.ExactArgs(1),
	RunE: runDepsInfo,
}

var typeFilter string

func init() {
	rootCmd.AddCommand(depsCmd)
	depsCmd.AddCommand(depsListCmd)
	depsCmd.AddCommand(depsInfoCmd)

	depsListCmd.Flags().StringVar(&typeFilter, "type", "", "filter by type (runtime, npm, apt, github-binary, custom)")
}

func runDepsList(cmd *cobra.Command, args []string) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTYPE\tDEFAULT\tDESCRIPTION")

	names := deps.List()
	for _, name := range names {
		spec := deps.Registry[name]
		if typeFilter != "" && string(spec.Type) != typeFilter {
			continue
		}
		desc := spec.Description
		if len(desc) > 40 {
			desc = desc[:37] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", name, spec.Type, spec.Default, desc)
	}
	w.Flush()
	return nil
}

func runDepsInfo(cmd *cobra.Command, args []string) error {
	name := args[0]
	spec, ok := deps.Registry[name]
	if !ok {
		// Try to suggest
		suggestions := []string{}
		for n := range deps.Registry {
			if strings.Contains(n, name) || strings.Contains(name, n) {
				suggestions = append(suggestions, n)
			}
		}
		msg := fmt.Sprintf("unknown dependency %q", name)
		if len(suggestions) > 0 {
			sort.Strings(suggestions)
			msg += fmt.Sprintf("\n\nDid you mean one of these?\n  %s", strings.Join(suggestions, "\n  "))
		}
		msg += "\n\nRun 'agent deps list' to see all available dependencies."
		return fmt.Errorf(msg)
	}

	fmt.Printf("Name:        %s\n", name)
	fmt.Printf("Type:        %s\n", spec.Type)
	if spec.Description != "" {
		fmt.Printf("Description: %s\n", spec.Description)
	}
	if spec.Default != "" {
		fmt.Printf("Default:     %s\n", spec.Default)
	}
	if len(spec.Versions) > 0 {
		fmt.Printf("Versions:    %s\n", strings.Join(spec.Versions, ", "))
	}
	if len(spec.Requires) > 0 {
		fmt.Printf("Requires:    %s\n", strings.Join(spec.Requires, ", "))
	}

	fmt.Println()
	fmt.Println("Usage in agent.yaml:")
	if spec.Default != "" {
		fmt.Printf("  dependencies:\n    - %s        # uses default version %s\n", name, spec.Default)
		fmt.Printf("    - %s@%s    # explicit version\n", name, spec.Default)
	} else {
		fmt.Printf("  dependencies:\n    - %s\n", name)
	}

	return nil
}
