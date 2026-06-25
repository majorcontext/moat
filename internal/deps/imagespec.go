package deps

import (
	"sort"

	"github.com/majorcontext/moat/internal/providers/claude"
)

// ImageSpec captures all options needed for image resolution, tag generation,
// and Dockerfile generation. A single ImageSpec is constructed once and passed
// through the entire image pipeline.
type ImageSpec struct {
	// BaseImage overrides the default base image selection. When set, the
	// Dockerfile uses this image as the FROM line instead of auto-selecting
	// based on runtime dependencies. Must be Debian-based.
	BaseImage string
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

	// HasNamedVolumes indicates the run mounts at least one type: volume (native
	// Docker named volume). Such volumes are created root-owned; moat-init's
	// in-container chown is what makes them writable by the non-root run user on
	// root-entrypoint runtimes, so their presence requires the moat-init entrypoint.
	HasNamedVolumes bool

	// Hooks contains user-defined lifecycle hook commands.
	Hooks *HooksConfig

	// NeedsWorkspaceVolume indicates the run uses volume-mode workspaces, which
	// require the moat-init entrypoint to populate the named volume from the
	// read-only staging bind and chown it (both as root, before the privilege
	// drop). Without the entrypoint, populate_workspace_volume never runs and the
	// volume is left empty, so this forces both a custom image and the init script.
	NeedsWorkspaceVolume bool
}

// NeedsCustomImage reports whether any option requires building a custom image.
func (s *ImageSpec) NeedsCustomImage(hasDeps bool) bool {
	if s == nil {
		return hasDeps
	}
	hasHooks := s.Hooks != nil && (s.Hooks.PostBuild != "" || s.Hooks.PostBuildRoot != "" || s.Hooks.PreRun != "")
	return hasDeps || s.BaseImage != "" || s.NeedsSSH || len(s.InitProviders) > 0 ||
		s.NeedsFirewall || s.NeedsInitFiles || s.NeedsClipboard ||
		len(s.ClaudePlugins) > 0 || hasHooks || s.NeedsWorkspaceVolume
}

// needsInit returns whether the moat-init entrypoint script is required.
// dockerMode must be passed separately because it comes from dependency
// categorization, not from ImageSpec.
//
// NeedsFirewall is included because strict network.policy on Apple containers
// relies on moat-init.sh to write synthetic hostnames (moat-proxy, moat-host)
// to /etc/hosts via MOAT_EXTRA_HOSTS. Without the entrypoint, HTTP_PROXY
// points at an unresolvable hostname and the policy fails open.
//
// HasNamedVolumes is included because a custom image otherwise runs directly as
// the non-root user (USER moatuser) with no entrypoint; named volumes are created
// root-owned, and moat-init is what chowns them so that user can write — without
// it the run hits EACCES on first write to the volume.
func (s *ImageSpec) needsInit(dockerMode DockerMode) bool {
	if s == nil {
		return dockerMode != ""
	}
	hasPreRun := s.Hooks != nil && s.Hooks.PreRun != ""
	return s.NeedsSSH || len(s.InitProviders) > 0 || s.NeedsClipboard ||
		dockerMode != "" || hasPreRun || s.NeedsGitIdentity || s.NeedsInitFiles ||
		s.NeedsFirewall || s.HasNamedVolumes || s.NeedsWorkspaceVolume
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
