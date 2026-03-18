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
// moat.yaml to see and fix the actual invalid value.

// PluginSnippetResult holds the Dockerfile snippet and optional script context file.
type PluginSnippetResult struct {
	// DockerfileSnippet is the Dockerfile text to append (COPY + RUN).
	DockerfileSnippet string
	// ScriptName is the context file name (empty if no plugins).
	ScriptName string
	// ScriptContent is the shell script content (nil if no plugins).
	ScriptContent []byte
}

// GenerateDockerfileSnippet generates Dockerfile commands for Claude Code plugin installation.
// Returns an empty result if no marketplaces or plugins are configured.
//
// Plugin install commands are written to a separate shell script (returned as
// a context file) rather than inline Dockerfile RUN steps. This keeps the
// Dockerfile under the Apple containers builder's ~16KB gRPC transport limit
// which causes "Transport became inactive" errors for larger Dockerfiles.
//
// The containerUser parameter specifies the user to install plugins as. This is used in
// Dockerfile USER and WORKDIR commands. Callers must ensure this is a safe, validated
// value (e.g., hardcoded "moatuser") since it's inserted directly into the Dockerfile.
// The function does not validate this parameter to allow flexibility in user naming.
func GenerateDockerfileSnippet(marketplaces []MarketplaceConfig, plugins []string, containerUser string) PluginSnippetResult {
	if len(marketplaces) == 0 && len(plugins) == 0 {
		return PluginSnippetResult{}
	}

	// Sort marketplaces for deterministic output
	sortedMarketplaces := make([]MarketplaceConfig, len(marketplaces))
	copy(sortedMarketplaces, marketplaces)
	sort.Slice(sortedMarketplaces, func(i, j int) bool {
		return sortedMarketplaces[i].Name < sortedMarketplaces[j].Name
	})

	// Sort plugins for deterministic output
	sortedPlugins := make([]string, len(plugins))
	copy(sortedPlugins, plugins)
	sort.Strings(sortedPlugins)

	// Build the install script
	var script strings.Builder
	script.WriteString("#!/bin/bash\n")
	script.WriteString("set -e\n")
	script.WriteString("# Auto-generated Claude Code plugin installer\n")
	script.WriteString("# Failures are fatal — the build stops if any marketplace or plugin install fails.\n\n")
	// Ensure the Claude CLI is on PATH. The native installer places the binary
	// in ~/.claude/local/bin/ which may not be in PATH during image build.
	script.WriteString(fmt.Sprintf("export PATH=\"/home/%s/.claude/local/bin:/home/%s/.local/bin:$PATH\"\n\n", containerUser, containerUser))
	script.WriteString("failures=0\n\n")

	// Add marketplaces - failures are fatal (marketplaces are prerequisites for plugins)
	for _, m := range sortedMarketplaces {
		if m.Repo == "" {
			continue
		}
		// Validate repo format to prevent command injection
		if !validMarketplaceRepo.MatchString(m.Repo) {
			script.WriteString(fmt.Sprintf("echo 'ERROR: Invalid marketplace repo format: %s, skipping' >&2\n", m.Name))
			script.WriteString("failures=$((failures + 1))\n")
			continue
		}
		script.WriteString(fmt.Sprintf("if claude plugin marketplace add %s; then\n", m.Repo))
		script.WriteString(fmt.Sprintf("  echo 'Added marketplace %s'\n", m.Name))
		script.WriteString("else\n")
		script.WriteString(fmt.Sprintf("  echo 'ERROR: Failed to add marketplace %s' >&2\n", m.Name))
		script.WriteString("  failures=$((failures + 1))\n")
		script.WriteString("fi\n")
	}

	// Install plugins - failures are fatal (user explicitly requested them)
	for _, plugin := range sortedPlugins {
		// Validate plugin format to prevent command injection
		if !validPluginKey.MatchString(plugin) {
			script.WriteString(fmt.Sprintf("echo 'ERROR: Invalid plugin format: %s (expected plugin-name@marketplace-name), skipping' >&2\n", plugin))
			script.WriteString("failures=$((failures + 1))\n")
			continue
		}
		script.WriteString(fmt.Sprintf("if claude plugin install %s; then\n", plugin))
		script.WriteString(fmt.Sprintf("  echo 'Installed plugin %s'\n", plugin))
		script.WriteString("else\n")
		script.WriteString(fmt.Sprintf("  echo 'ERROR: Failed to install plugin %s' >&2\n", plugin))
		script.WriteString("  failures=$((failures + 1))\n")
		script.WriteString("fi\n")
	}

	// Exit with failure if anything went wrong
	script.WriteString("\nif [ \"$failures\" -gt 0 ]; then\n")
	script.WriteString("  echo \"ERROR: $failures plugin operation(s) failed\" >&2\n")
	script.WriteString("  exit 1\n")
	script.WriteString("fi\n")

	// Build the Dockerfile snippet that COPY's and runs the script
	var dockerfile strings.Builder
	dockerfile.WriteString("# Claude Code plugins\n")
	dockerfile.WriteString(fmt.Sprintf("USER %s\n", containerUser))
	dockerfile.WriteString(fmt.Sprintf("WORKDIR /home/%s\n", containerUser))
	dockerfile.WriteString(fmt.Sprintf("COPY --chown=%s claude-plugins.sh /tmp/claude-plugins.sh\n", containerUser))
	dockerfile.WriteString("RUN bash /tmp/claude-plugins.sh && rm /tmp/claude-plugins.sh\n\n")

	return PluginSnippetResult{
		DockerfileSnippet: dockerfile.String(),
		ScriptName:        "claude-plugins.sh",
		ScriptContent:     []byte(script.String()),
	}
}
