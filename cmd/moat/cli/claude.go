package cli

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/andybons/moat/internal/claude"
	"github.com/andybons/moat/internal/config"
	"github.com/spf13/cobra"
)

var claudeCmd = &cobra.Command{
	Use:   "claude",
	Short: "Manage Claude Code plugins",
	Long: `Manage Claude Code plugins for Moat runs.

Plugins are configured in agent.yaml and baked into container images at build time.
Use 'moat claude plugins list' to see configured plugins.

Configuration sources (lowest to highest precedence):
  1. ~/.claude/settings.json          Claude's native user settings
  2. ~/.moat/claude/settings.json     User defaults for moat runs
  3. .claude/settings.json            Project defaults (in workspace)
  4. agent.yaml claude.*              Run-specific overrides`,
}

var pluginsCmd = &cobra.Command{
	Use:   "plugins",
	Short: "Manage Claude Code plugins",
}

var pluginsListCmd = &cobra.Command{
	Use:   "list [workspace]",
	Short: "List configured plugins",
	Long: `List all plugins that will be baked into the container image.

Shows the merged plugin configuration from all sources with indication of
where each plugin setting comes from.

Note: Plugins are installed during image build via 'claude plugin install'.
Changes to plugin configuration require rebuilding the image with --rebuild.

Examples:
  # List plugins for current directory
  moat claude plugins list

  # List plugins for a specific workspace
  moat claude plugins list ./my-project`,
	Args: cobra.MaximumNArgs(1),
	RunE: runPluginsList,
}

func init() {
	rootCmd.AddCommand(claudeCmd)

	claudeCmd.AddCommand(pluginsCmd)
	pluginsCmd.AddCommand(pluginsListCmd)
}

func runPluginsList(cmd *cobra.Command, args []string) error {
	workspace := "."
	if len(args) > 0 {
		workspace = args[0]
	}

	absPath, err := filepath.Abs(workspace)
	if err != nil {
		return fmt.Errorf("resolving workspace path: %w", err)
	}

	// Load agent.yaml if present
	cfg, err := config.Load(absPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Load and merge all settings
	settings, err := claude.LoadAllSettings(absPath, cfg)
	if err != nil {
		return fmt.Errorf("loading settings: %w", err)
	}

	if len(settings.EnabledPlugins) == 0 && len(settings.ExtraKnownMarketplaces) == 0 {
		fmt.Println("No plugins configured.")
		fmt.Println("\nTo enable plugins, add to agent.yaml:")
		fmt.Println(`
  claude:
    marketplaces:
      my-marketplace:
        source: github
        repo: owner/marketplace-repo

    plugins:
      plugin-name@my-marketplace: true`)
		fmt.Println("\nPlugins are installed during image build. Use --rebuild after changing plugin configuration.")
		return nil
	}

	fmt.Printf("Plugins for %s:\n\n", absPath)

	if len(settings.EnabledPlugins) > 0 {
		// Sort plugin names for consistent output
		names := make([]string, 0, len(settings.EnabledPlugins))
		for name := range settings.EnabledPlugins {
			names = append(names, name)
		}
		sort.Strings(names)

		// Find max plugin name length for alignment
		maxLen := 0
		for _, name := range names {
			if len(name) > maxLen {
				maxLen = len(name)
			}
		}

		for _, name := range names {
			enabled := settings.EnabledPlugins[name]
			status := ""
			if !enabled {
				status = " (disabled)"
			}
			source := settings.PluginSources[name]
			fmt.Printf("  %-*s  %s%s\n", maxLen, name, source, status)
		}
	}

	// Show marketplaces that are explicitly configured
	if len(settings.ExtraKnownMarketplaces) > 0 {
		fmt.Printf("\nMarketplaces:\n\n")

		var marketNames []string
		for name := range settings.ExtraKnownMarketplaces {
			marketNames = append(marketNames, name)
		}
		sort.Strings(marketNames)

		// Find max marketplace name length for alignment
		maxMarketLen := 0
		for _, name := range marketNames {
			if len(name) > maxMarketLen {
				maxMarketLen = len(name)
			}
		}

		for _, name := range marketNames {
			entry := settings.ExtraKnownMarketplaces[name]
			source := settings.MarketplaceSources[name]
			fmt.Printf("  %-*s  %s  %s\n", maxMarketLen, name, formatMarketplaceSource(entry), source)
		}
	}

	fmt.Println("\nNote: Plugins are baked into images at build time. Use --rebuild to update.")

	return nil
}

func formatMarketplaceSource(entry claude.MarketplaceEntry) string {
	switch entry.Source.Source {
	case "directory":
		return entry.Source.Path
	case "git":
		return entry.Source.URL
	default:
		return entry.Source.Source
	}
}
