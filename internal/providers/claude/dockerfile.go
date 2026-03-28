package claude

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/majorcontext/moat/internal/log"
)

// MarketplaceConfig represents a Claude Code plugin marketplace for image building.
type MarketplaceConfig struct {
	Name       string // Marketplace name (e.g., "claude-plugins-official")
	Source     string // "github" or "git"
	Repo       string // Repository path (e.g., "anthropics/claude-plugins-official")
	PreCloned  string // PreCloned is the build-context-relative path prefix for a marketplace that was cloned on the host. When set, GenerateDockerfileSnippet will COPY the files instead of running claude plugin marketplace add.
	CommitTime string // CommitTime is the ISO 8601 timestamp of the last commit (for known_marketplaces.json).
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

// validName matches safe names for use in shell echo statements.
// Rejects characters like single quotes that could break shell syntax.
var validName = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// validPreClonedPath matches safe build-context-relative paths for COPY directives.
// Allows alphanumeric, dots, hyphens, underscores, and forward slashes only.
// Rejects spaces, newlines, shell metacharacters, and backslashes.
var validPreClonedPath = regexp.MustCompile(`^[a-zA-Z0-9._/-]+$`)

// validMarketplaceName matches safe marketplace names for use in filesystem paths.
// Allows alphanumeric, hyphens, and underscores only.
var validMarketplaceName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidMarketplaceName reports whether name is a safe marketplace name
// for use in filesystem paths and build-context keys.
func ValidMarketplaceName(name string) bool {
	return validMarketplaceName.MatchString(name)
}

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
	// ExtraContextFiles maps build-context-relative paths to file contents.
	// Used for known_marketplaces.json for pre-cloned marketplaces.
	ExtraContextFiles map[string][]byte
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

	// Separate pre-cloned and remote marketplaces.
	var preCloned []MarketplaceConfig
	var remote []MarketplaceConfig
	for _, m := range sortedMarketplaces {
		if m.PreCloned != "" {
			preCloned = append(preCloned, m)
		} else {
			remote = append(remote, m)
		}
	}

	// Build the install script
	var script strings.Builder
	script.WriteString("#!/bin/bash\n")
	script.WriteString("# Auto-generated Claude Code plugin installer\n")
	script.WriteString("# Failures are tracked and the script exits non-zero if any operation fails.\n\n")
	// Ensure the Claude CLI is on PATH. The native installer places the binary
	// in ~/.claude/local/bin/ which may not be in PATH during image build.
	script.WriteString(fmt.Sprintf("export PATH=\"/home/%s/.claude/local/bin:/home/%s/.local/bin:$PATH\"\n\n", containerUser, containerUser))
	// Skip plugin installation if Claude Code is not installed.
	// Host-level marketplace settings are loaded for all runs, but the claude
	// binary is only present when claude-code is a dependency or implicit (moat claude).
	script.WriteString("if ! command -v claude >/dev/null 2>&1; then\n")
	script.WriteString("  echo 'Claude Code not installed, skipping plugin setup'\n")
	script.WriteString("  exit 0\n")
	script.WriteString("fi\n\n")
	script.WriteString("failures=0\n")
	script.WriteString("failed_ops=\"\"\n\n")

	// Add remote marketplaces only — pre-cloned ones are COPYed into the image.
	// Failures are fatal (marketplaces are prerequisites for plugins).
	for _, m := range remote {
		if m.Repo == "" {
			continue
		}
		// Validate name for safe use in shell echo statements
		if !validName.MatchString(m.Name) {
			script.WriteString("echo 'ERROR: Invalid marketplace name, skipping'\n")
			script.WriteString("failures=$((failures + 1))\n")
			script.WriteString("failed_ops=\"$failed_ops marketplace:unknown\"\n")
			continue
		}
		// Validate repo format to prevent command injection
		if !validMarketplaceRepo.MatchString(m.Repo) {
			script.WriteString(fmt.Sprintf("echo 'ERROR: Invalid marketplace repo format: %s, skipping'\n", m.Name))
			script.WriteString("failures=$((failures + 1))\n")
			script.WriteString(fmt.Sprintf("failed_ops=\"$failed_ops marketplace:%s\"\n", m.Name))
			continue
		}
		script.WriteString(fmt.Sprintf("if claude plugin marketplace add %s; then\n", m.Repo))
		script.WriteString(fmt.Sprintf("  echo 'Added marketplace %s'\n", m.Name))
		script.WriteString("else\n")
		script.WriteString(fmt.Sprintf("  echo 'ERROR: Failed to add marketplace %s'\n", m.Name))
		script.WriteString("  failures=$((failures + 1))\n")
		script.WriteString(fmt.Sprintf("  failed_ops=\"$failed_ops marketplace:%s\"\n", m.Name))
		script.WriteString("fi\n")
	}

	// Install plugins - failures are fatal (user explicitly requested them)
	for _, plugin := range sortedPlugins {
		// Validate plugin format to prevent command injection.
		// validPluginKey only allows [a-zA-Z0-9_-]+@[a-zA-Z0-9_-]+,
		// so validated plugins are safe in shell echo statements.
		if !validPluginKey.MatchString(plugin) {
			// Don't embed the invalid value — it failed validation and may contain shell metacharacters.
			script.WriteString("echo 'ERROR: Invalid plugin format (expected plugin-name@marketplace-name), skipping'\n")
			script.WriteString("failures=$((failures + 1))\n")
			script.WriteString("failed_ops=\"$failed_ops plugin:invalid\"\n")
			continue
		}
		script.WriteString(fmt.Sprintf("if claude plugin install %s; then\n", plugin))
		script.WriteString(fmt.Sprintf("  echo 'Installed plugin %s'\n", plugin))
		script.WriteString("else\n")
		script.WriteString(fmt.Sprintf("  echo 'ERROR: Failed to install plugin %s'\n", plugin))
		script.WriteString("  failures=$((failures + 1))\n")
		script.WriteString(fmt.Sprintf("  failed_ops=\"$failed_ops plugin:%s\"\n", plugin))
		script.WriteString("fi\n")
	}

	// Exit with failure if anything went wrong
	script.WriteString("\nif [ \"$failures\" -gt 0 ]; then\n")
	script.WriteString("  echo \"ERROR: $failures plugin operation(s) failed:$failed_ops\"\n")
	script.WriteString("  exit 1\n")
	script.WriteString("fi\n")

	// Build the Dockerfile snippet that COPY's and runs the script
	var dockerfile strings.Builder
	dockerfile.WriteString("# Claude Code plugins\n")
	dockerfile.WriteString(fmt.Sprintf("USER %s\n", containerUser))
	dockerfile.WriteString(fmt.Sprintf("WORKDIR /home/%s\n", containerUser))

	// COPY pre-cloned marketplace tar archives and extract them.
	// Each marketplace is packaged as a single flat tar file to work around an
	// Apple container builder bug where nested directory contents in the build
	// context are not transferred to the builder VM. Single flat files transfer
	// correctly on all builders.
	var validPreCloned []MarketplaceConfig
	for _, m := range preCloned {
		if !validMarketplaceName.MatchString(m.Name) {
			log.Warn("invalid marketplace name for pre-cloned entry, skipping")
			continue
		}
		if strings.Contains(m.PreCloned, "..") || !validPreClonedPath.MatchString(m.PreCloned) {
			log.Warn("invalid pre-cloned path for marketplace, skipping", "name", m.Name)
			continue
		}
		validPreCloned = append(validPreCloned, m)
		destDir := fmt.Sprintf("/home/%s/.claude/plugins/marketplaces/%s", containerUser, m.Name)
		dockerfile.WriteString(fmt.Sprintf("COPY --chown=%s %s /tmp/%s\n", containerUser, m.PreCloned, m.PreCloned))
		dockerfile.WriteString(fmt.Sprintf("RUN mkdir -p %s && tar xf /tmp/%s -C %s && rm /tmp/%s\n",
			destDir, m.PreCloned, destDir, m.PreCloned))
	}

	// Generate known_marketplaces.json for pre-cloned marketplaces.
	var extraFiles map[string][]byte
	if len(validPreCloned) > 0 {
		pcList := make([]PreClonedMarketplace, len(validPreCloned))
		for i, m := range validPreCloned {
			pcList[i] = PreClonedMarketplace{
				Name:        m.Name,
				Source:      m.Source,
				Repo:        m.Repo,
				LastUpdated: m.CommitTime,
			}
		}
		knownJSON, err := GenerateKnownMarketplaces(pcList, containerUser)
		if err != nil {
			log.Warn("could not generate known_marketplaces.json; pre-cloned marketplaces may not be recognized",
				"error", err)
		} else {
			extraFiles = map[string][]byte{
				"known-marketplaces.json": knownJSON,
			}
			dockerfile.WriteString(fmt.Sprintf("COPY --chown=%s known-marketplaces.json /home/%s/.claude/plugins/known_marketplaces.json\n",
				containerUser, containerUser))
		}
	}

	dockerfile.WriteString(fmt.Sprintf("COPY --chown=%s claude-plugins.sh /tmp/claude-plugins.sh\n", containerUser))
	dockerfile.WriteString("RUN bash /tmp/claude-plugins.sh && rm /tmp/claude-plugins.sh\n\n")

	return PluginSnippetResult{
		DockerfileSnippet: dockerfile.String(),
		ScriptName:        "claude-plugins.sh",
		ScriptContent:     []byte(script.String()),
		ExtraContextFiles: extraFiles,
	}
}
