package claude

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// MarketplaceConfig represents a Claude Code plugin marketplace for image building.
type MarketplaceConfig struct {
	Name   string // Marketplace name (e.g., "claude-plugins-official")
	Source string // "github" or "git"
	Repo   string // Repository path (e.g., "anthropics/claude-plugins-official")
}

// validMarketplaceRepo matches valid marketplace repo formats:
// - owner/repo (GitHub shorthand)
// - git@host:path (SSH URLs)
// - https://host/path (HTTPS URLs)
// Rejects shell metacharacters to prevent command injection.
var validMarketplaceRepo = regexp.MustCompile(`^[a-zA-Z0-9._@:/-]+$`)

// validPluginKey matches plugin@marketplace format.
// Allows alphanumeric, hyphens, underscores, and exactly one @.
var validPluginKey = regexp.MustCompile(`^[a-zA-Z0-9_-]+@[a-zA-Z0-9_-]+$`)

// Note on error handling: When validation fails, error messages include the
// marketplace name or plugin key (which are user-visible identifiers) but NOT
// the invalid repo/value itself. This prevents potentially malicious content
// from appearing in the Dockerfile. Users can look up the name in their
// agent.yaml to see and fix the actual invalid value.

// GenerateDockerfileSnippet generates Dockerfile commands for Claude Code plugin installation.
// Returns an empty string if no marketplaces or plugins are configured.
//
// The containerUser parameter specifies the user to install plugins as. This is used in
// Dockerfile USER and WORKDIR commands. Callers must ensure this is a safe, validated
// value (e.g., hardcoded "moatuser") since it's inserted directly into the Dockerfile.
// The function does not validate this parameter to allow flexibility in user naming.
func GenerateDockerfileSnippet(marketplaces []MarketplaceConfig, plugins []string, containerUser string) string {
	if len(marketplaces) == 0 && len(plugins) == 0 {
		return ""
	}

	var b strings.Builder

	b.WriteString("# Claude Code plugins\n")
	b.WriteString(fmt.Sprintf("USER %s\n", containerUser))
	b.WriteString(fmt.Sprintf("WORKDIR /home/%s\n", containerUser))

	// Sort marketplaces for deterministic output
	sortedMarketplaces := make([]MarketplaceConfig, len(marketplaces))
	copy(sortedMarketplaces, marketplaces)
	sort.Slice(sortedMarketplaces, func(i, j int) bool {
		return sortedMarketplaces[i].Name < sortedMarketplaces[j].Name
	})

	// Add marketplaces
	for _, m := range sortedMarketplaces {
		if m.Repo == "" {
			continue
		}
		// Validate repo format to prevent command injection
		if !validMarketplaceRepo.MatchString(m.Repo) {
			b.WriteString(fmt.Sprintf("RUN echo 'ERROR: Invalid marketplace repo format: %s' && exit 1\n", m.Name))
			continue
		}
		b.WriteString(fmt.Sprintf("RUN claude plugin marketplace add %s || (echo 'Failed to add marketplace %s. Check SSH grants if this is a private repository.' && exit 1)\n", m.Repo, m.Name))
	}

	// Sort plugins for deterministic output
	sortedPlugins := make([]string, len(plugins))
	copy(sortedPlugins, plugins)
	sort.Strings(sortedPlugins)

	// Install plugins
	for _, plugin := range sortedPlugins {
		// Validate plugin format to prevent command injection
		if !validPluginKey.MatchString(plugin) {
			b.WriteString(fmt.Sprintf("RUN echo 'ERROR: Invalid plugin format: %s (expected plugin-name@marketplace-name)' && exit 1\n", plugin))
			continue
		}
		b.WriteString(fmt.Sprintf("RUN claude plugin install %s || (echo 'Failed to install plugin %s. Verify the plugin exists in the marketplace.' && exit 1)\n", plugin, plugin))
	}

	b.WriteString("USER root\n\n")

	return b.String()
}
