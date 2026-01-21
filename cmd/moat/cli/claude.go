package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andybons/moat/internal/claude"
	"github.com/andybons/moat/internal/config"
	"github.com/spf13/cobra"
)

var claudeCmd = &cobra.Command{
	Use:   "claude",
	Short: "Manage Claude Code plugins and marketplaces",
	Long: `Manage Claude Code plugins and marketplaces for Moat runs.

Moat manages plugin fetching and caching on the host, mounts a read-only cache
into containers, and generates Claude-native configuration.

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
	Long: `List all plugins that will be available in a run.

Shows the merged plugin configuration from all sources with indication of
where each plugin setting comes from.

Examples:
  # List plugins for current directory
  moat claude plugins list

  # List plugins for a specific workspace
  moat claude plugins list ./my-project`,
	Args: cobra.MaximumNArgs(1),
	RunE: runPluginsList,
}

var marketplacesCmd = &cobra.Command{
	Use:   "marketplace",
	Short: "Manage plugin marketplaces",
}

var marketplacesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured marketplaces",
	Long: `List all configured marketplaces and their status.

Shows marketplaces from:
  - ~/.claude/settings.json (Claude's native user settings)
  - ~/.moat/claude/settings.json
  - .claude/settings.json (in workspace)
  - agent.yaml claude.marketplaces`,
	Args: cobra.MaximumNArgs(1),
	RunE: runMarketplacesList,
}

var marketplacesUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update all marketplace caches",
	Long: `Pull latest changes for all cached marketplaces.

This updates the local clones of marketplace repositories. Plugins are
resolved from these caches when starting runs.

Examples:
  # Update all marketplaces
  moat claude marketplace update`,
	RunE: runMarketplacesUpdate,
}

func init() {
	rootCmd.AddCommand(claudeCmd)

	claudeCmd.AddCommand(pluginsCmd)
	pluginsCmd.AddCommand(pluginsListCmd)

	claudeCmd.AddCommand(marketplacesCmd)
	marketplacesCmd.AddCommand(marketplacesListCmd)
	marketplacesCmd.AddCommand(marketplacesUpdateCmd)
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

	if len(settings.EnabledPlugins) == 0 {
		fmt.Println("No plugins configured.")
		fmt.Println("\nTo enable plugins, add to .claude/settings.json or agent.yaml:")
		fmt.Println(`
  # .claude/settings.json
  {
    "enabledPlugins": {
      "plugin-name@marketplace": true
    }
  }

  # agent.yaml
  claude:
    plugins:
      plugin-name@marketplace: true`)
		return nil
	}

	fmt.Printf("Plugins for %s:\n\n", absPath)

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

	// Only show marketplaces that are explicitly configured
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

	return nil
}

func runMarketplacesList(cmd *cobra.Command, args []string) error {
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

	// Get cache directory
	cacheDir, err := claude.DefaultCacheDir()
	if err != nil {
		return fmt.Errorf("getting cache directory: %w", err)
	}
	marketplaceManager := claude.NewMarketplaceManager(cacheDir)

	if len(settings.ExtraKnownMarketplaces) == 0 {
		fmt.Println("No marketplaces configured.")
		fmt.Println("\nTo add a marketplace, add to .claude/settings.json or agent.yaml:")
		fmt.Println(`
  # .claude/settings.json
  {
    "extraKnownMarketplaces": {
      "my-marketplace": {
        "source": {
          "source": "git",
          "url": "https://github.com/org/plugins.git"
        }
      }
    }
  }

  # agent.yaml
  claude:
    marketplaces:
      my-marketplace:
        source: github
        repo: org/plugins`)
		return nil
	}

	fmt.Printf("Marketplaces:\n\n")

	// Sort marketplace names for consistent output
	names := make([]string, 0, len(settings.ExtraKnownMarketplaces))
	for name := range settings.ExtraKnownMarketplaces {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		entry := settings.ExtraKnownMarketplaces[name]
		source := settings.MarketplaceSources[name]
		fmt.Printf("  %s\n", name)
		fmt.Printf("    Source: %s\n", formatMarketplaceSource(entry))
		fmt.Printf("    From: %s\n", source)

		// Check if cached
		cachedPath := marketplaceManager.MarketplacePath(name)
		if entry.Source.Source == "directory" {
			cachedPath = entry.Source.Path
		}
		if _, err := os.Stat(cachedPath); err == nil {
			fmt.Printf("    Cached: %s\n", cachedPath)
		} else {
			fmt.Printf("    Cached: not yet cloned\n")
		}

		// Check if SSH access is required
		if entry.Source.Source == "git" && claude.IsSSHURL(entry.Source.URL) {
			host := claude.ExtractHost(entry.Source.URL)
			fmt.Printf("    Requires: ssh:%s grant\n", host)
		}
		fmt.Println()
	}

	return nil
}

func runMarketplacesUpdate(cmd *cobra.Command, args []string) error {
	cacheDir, err := claude.DefaultCacheDir()
	if err != nil {
		return fmt.Errorf("getting cache directory: %w", err)
	}

	marketplacesDir := filepath.Join(cacheDir, "marketplaces")
	entries, err := os.ReadDir(marketplacesDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No marketplaces cached yet.")
			return nil
		}
		return fmt.Errorf("reading marketplaces directory: %w", err)
	}

	if len(entries) == 0 {
		fmt.Println("No marketplaces cached yet.")
		return nil
	}

	fmt.Println("Updating marketplaces...")
	fmt.Println()

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()

		// Skip entries with path traversal characters
		if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
			continue
		}

		path := filepath.Join(marketplacesDir, name)

		// Check if it's a git repo
		if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
			continue
		}

		fmt.Printf("  %s: ", name)

		// Run git pull
		gitCmd := exec.Command("git", "pull", "--ff-only")
		gitCmd.Dir = path
		if err := gitCmd.Run(); err != nil {
			fmt.Printf("error: %v\n", err)
		} else {
			fmt.Println("updated")
		}
	}

	return nil
}

func formatMarketplaceSource(entry claude.MarketplaceEntry) string {
	switch entry.Source.Source {
	case "directory":
		return entry.Source.Path
	case "git":
		return entry.Source.URL
	default:
		// Try to serialize as JSON for unknown formats
		b, _ := json.Marshal(entry.Source)
		return string(b)
	}
}
