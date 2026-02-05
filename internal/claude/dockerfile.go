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
//
// The needsRootAfter parameter determines whether to end with `USER root`. Set to true
// if root operations follow (dynamic deps, SSH hosts, etc.), false if entrypoint is next.
func GenerateDockerfileSnippet(marketplaces []MarketplaceConfig, plugins []string, containerUser string, needsRootAfter bool) string {
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

	// Add marketplaces in a single RUN layer for faster builds
	// Failures are non-fatal (private repos may not be accessible during build)
	validMarketplaces := make([]MarketplaceConfig, 0, len(sortedMarketplaces))
	for _, m := range sortedMarketplaces {
		if m.Repo == "" {
			continue
		}
		if !validMarketplaceRepo.MatchString(m.Repo) {
			b.WriteString(fmt.Sprintf("RUN echo 'WARNING: Invalid marketplace repo format: %s, skipping'\n", m.Name))
			continue
		}
		validMarketplaces = append(validMarketplaces, m)
	}

	if len(validMarketplaces) > 0 {
		b.WriteString("RUN ")
		for i, m := range validMarketplaces {
			if i > 0 {
				b.WriteString(" && \\\n    ")
			}
			b.WriteString(fmt.Sprintf("(claude plugin marketplace add %s && echo 'Added marketplace %s' || echo 'WARNING: Could not add marketplace %s (may be private or inaccessible during build)')", m.Repo, m.Name, m.Name))
		}
		b.WriteString("\n")
	}

	// Sort plugins for deterministic output
	sortedPlugins := make([]string, len(plugins))
	copy(sortedPlugins, plugins)
	sort.Strings(sortedPlugins)

	// Install plugins in a single RUN layer for faster builds
	// Failures are non-fatal (plugins from inaccessible marketplaces will fail)
	validPlugins := make([]string, 0, len(sortedPlugins))
	for _, plugin := range sortedPlugins {
		if !validPluginKey.MatchString(plugin) {
			b.WriteString(fmt.Sprintf("RUN echo 'WARNING: Invalid plugin format: %s (expected plugin-name@marketplace-name), skipping'\n", plugin))
			continue
		}
		validPlugins = append(validPlugins, plugin)
	}

	if len(validPlugins) > 0 {
		b.WriteString("RUN ")
		for i, plugin := range validPlugins {
			if i > 0 {
				b.WriteString(" && \\\n    ")
			}
			b.WriteString(fmt.Sprintf("(claude plugin install %s && echo 'Installed plugin %s' || echo 'WARNING: Could not install plugin %s (marketplace may be inaccessible)')", plugin, plugin, plugin))
		}
		b.WriteString("\n")
	}

	// Only switch back to root if root operations follow
	if needsRootAfter {
		b.WriteString("USER root\n")
	}
	b.WriteString("\n")

	return b.String()
}
