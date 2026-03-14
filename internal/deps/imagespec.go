package deps

import (
	"sort"

	"github.com/majorcontext/moat/internal/providers/claude"
)

// ImageSpec captures all options needed for image resolution, tag generation,
// and Dockerfile generation. A single ImageSpec is constructed once and passed
// through the entire image pipeline.
type ImageSpec struct {
	// NeedsSSH indicates SSH grants are present and the image needs
	// openssh-client, socat, and the moat-init entrypoint for agent forwarding.
	NeedsSSH bool

	// SSHHosts lists the hosts for which SSH access is granted (e.g., "github.com").
	// Known host keys will be added to /etc/ssh/ssh_known_hosts for these hosts.
	// Used only by Dockerfile generation.
	SSHHosts []string

	// InitProviders lists agent provider names (e.g., "claude", "codex", "gemini")
	// that need configuration files staged at container startup. Each entry
	// triggers the moat-init entrypoint and contributes to the image tag hash.
	InitProviders []string

	// NeedsFirewall indicates that iptables is needed for strict network
	// policy enforcement.
	NeedsFirewall bool

	// NeedsGitIdentity indicates the host's git identity should be injected
	// into the container. Used only by Dockerfile generation.
	NeedsGitIdentity bool

	// NeedsInitFiles indicates that providers have init files to write at
	// container startup.
	NeedsInitFiles bool

	// NeedsClipboard indicates the image needs Xvfb and xclip for host
	// clipboard bridging. Adds xvfb and xclip apt packages, and starts
	// Xvfb :99 in the moat-init entrypoint.
	NeedsClipboard bool

	// UseBuildKit enables BuildKit-specific features like cache mounts.
	// Used only by Dockerfile generation. Defaults to false if nil.
	UseBuildKit *bool

	// ClaudeMarketplaces are plugin marketplaces to register during image build.
	// Used only by Dockerfile generation.
	ClaudeMarketplaces []claude.MarketplaceConfig

	// ClaudePlugins are plugins to install during image build.
	// Format: "plugin-name@marketplace-name"
	ClaudePlugins []string

	// Hooks contains user-defined lifecycle hook commands.
	Hooks *HooksConfig
}

// NeedsCustomImage reports whether any option requires building a custom image.
func (s *ImageSpec) NeedsCustomImage(hasDeps bool) bool {
	if s == nil {
		return hasDeps
	}
	hasHooks := s.Hooks != nil && (s.Hooks.PostBuild != "" || s.Hooks.PostBuildRoot != "" || s.Hooks.PreRun != "")
	return hasDeps || s.NeedsSSH || len(s.InitProviders) > 0 ||
		s.NeedsFirewall || s.NeedsInitFiles || s.NeedsClipboard ||
		len(s.ClaudePlugins) > 0 || hasHooks
}

// needsInit returns whether the moat-init entrypoint script is required.
// dockerMode must be passed separately because it comes from dependency
// categorization, not from ImageSpec.
func (s *ImageSpec) needsInit(dockerMode DockerMode) bool {
	if s == nil {
		return dockerMode != ""
	}
	hasPreRun := s.Hooks != nil && s.Hooks.PreRun != ""
	return s.NeedsSSH || len(s.InitProviders) > 0 || s.NeedsClipboard ||
		dockerMode != "" || hasPreRun || s.NeedsGitIdentity || s.NeedsInitFiles
}

// initProviderHashComponents returns sorted hash strings for InitProviders.
// Each provider contributes a "name:init" entry for deterministic image tagging.
func (s *ImageSpec) initProviderHashComponents() []string {
	if s == nil || len(s.InitProviders) == 0 {
		return nil
	}
	tags := make([]string, len(s.InitProviders))
	for i, name := range s.InitProviders {
		tags[i] = name + ":init"
	}
	sort.Strings(tags)
	return tags
}

// useBuildKit returns whether to use BuildKit features.
// Defaults to false if UseBuildKit is nil, since BuildKit availability
// cannot be assumed — the Docker legacy builder fails to parse
// BuildKit-specific syntax like --mount=type=cache.
func (s *ImageSpec) useBuildKit() bool {
	if s == nil || s.UseBuildKit == nil {
		return false
	}
	return *s.UseBuildKit
}
