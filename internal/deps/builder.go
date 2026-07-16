// internal/deps/builder.go
package deps

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/majorcontext/moat/internal/initbin"
	"github.com/majorcontext/moat/internal/providers/pi"
)

// ImageTag generates a deterministic image tag for a set of dependencies.
func ImageTag(deps []Dependency, opts *ImageSpec) string {
	if opts == nil {
		opts = &ImageSpec{}
	}

	// Sort deps for deterministic ordering
	sorted := make([]string, len(deps))
	for i, d := range deps {
		v := d.Version
		if v == "" {
			spec, _ := GetSpec(d.Name)
			v = spec.Default
		}
		key := d.Name + "@" + v
		// Include DockerMode in hash to differentiate docker:host vs docker:dind
		if d.DockerMode != "" {
			key += ":" + string(d.DockerMode)
		}
		sorted[i] = key
	}
	sort.Strings(sorted)

	// Build the hash input
	hashInput := strings.Join(sorted, ",")
	if opts.BaseImage != "" {
		hashInput += ",base:" + opts.BaseImage
	}
	if opts.NeedsSSH {
		hashInput += ",ssh:agent"
	}
	for _, tag := range opts.initProviderHashComponents() {
		hashInput += "," + tag
	}
	if opts.NeedsFirewall {
		hashInput += ",firewall:iptables"
	}
	if opts.NeedsInitFiles {
		hashInput += ",init-files"
	}
	if opts.NeedsClipboard {
		hashInput += ",clipboard:xvfb"
	}

	// When the moat-init entrypoint is used, hash its contents so that
	// changes to any entrypoint piece invalidate cached images. Without this,
	// users on runtimes without --add-host (Apple) can end up running stale
	// images that lack critical initialization logic. Mirror the conditions
	// in needsInit() plus any dep-driven DockerMode.
	dockerModePresent := false
	for _, d := range deps {
		if d.DockerMode != "" {
			dockerModePresent = true
			break
		}
	}
	if dockerModePresent || opts.needsInit("") {
		hashInput += "," + initHashComponent()
	}

	// Include plugins in hash (different plugins = different image).
	// Note: Plugin format validation happens in claude.GenerateDockerfileSnippet()
	// during Dockerfile generation. Invalid plugins will cause the build to fail
	// with a clear error message rather than silently being included.
	if len(opts.ClaudePlugins) > 0 {
		sortedPlugins := make([]string, len(opts.ClaudePlugins))
		copy(sortedPlugins, opts.ClaudePlugins)
		sort.Strings(sortedPlugins)
		for _, p := range sortedPlugins {
			hashInput += ",plugin:" + p
		}
	}

	// Content-hash the exact Pi bake script (safe settings + package installs) so
	// any change to the baked defaults OR the declared packages invalidates cached
	// images automatically — no manual version bump (mirrors the moat-init hashing
	// above). Gated on PiBakeSettings so PiPackages only perturb the tag when they
	// will actually be installed.
	if opts.PiBakeSettings {
		piScript := pi.GenerateDockerfileSnippet(opts.PiPackages, containerUser).ScriptContent
		ph := sha256.Sum256(piScript)
		hashInput += ",pi-bake:" + hex.EncodeToString(ph[:])[:8]
	}

	// Include hooks in hash (different hooks = different image)
	if opts.Hooks != nil {
		if opts.Hooks.PostBuild != "" {
			hashInput += ",hook:post_build:" + opts.Hooks.PostBuild
		}
		if opts.Hooks.PostBuildRoot != "" {
			hashInput += ",hook:post_build_root:" + opts.Hooks.PostBuildRoot
		}
		if opts.Hooks.PreRun != "" {
			hashInput += ",hook:pre_run:" + opts.Hooks.PreRun
		}
	}

	// Hash the combined input
	// Use 16 chars (64 bits) for sufficiently low collision probability
	// while keeping tags readable. 12 chars (48 bits) has ~0.1% collision
	// risk at 10k images; 16 chars reduces this to ~0.00001%.
	h := sha256.Sum256([]byte(hashInput))
	hash := hex.EncodeToString(h[:])[:16]

	return "moat/run:" + hash
}

// initHashComponent is the cache-key contribution of the moat-init
// entrypoint. It covers every piece writeEntrypoint ships: the shell script,
// the dispatcher, and the embedded Go binary — so a change to any of them
// re-keys freshly built images.
//
// The "moat-init-v2" label is a deliberate salt bump: images cached before
// the dispatcher existed hashed only the script under the "moat-init" label,
// and a warm-cache lookup must not resolve to a pre-dispatcher image that
// lacks the moat-init-go/moat-init-sh split. Note this re-keys the `moat
// run` build/lookup path only — a workflow pinning a concrete moat/run:<hash>
// tag still resolves the old image and must re-tag/rebuild at cutover.
func initHashComponent() string {
	h := sha256.New()
	h.Write([]byte(MoatInitScript))
	h.Write([]byte(MoatInitDispatcher))
	h.Write(initbin.Binary())
	return "moat-init-v2:" + hex.EncodeToString(h.Sum(nil))[:8]
}
