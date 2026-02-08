// internal/deps/builder.go
package deps

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// ImageTagOptions configures image tag generation.
type ImageTagOptions struct {
	// NeedsSSH indicates the image needs SSH packages and init script.
	NeedsSSH bool

	// NeedsClaudeInit indicates the image needs the init script for Claude setup.
	NeedsClaudeInit bool

	// NeedsCodexInit indicates the image needs the init script for Codex setup.
	NeedsCodexInit bool

	// NeedsGeminiInit indicates the image needs the init script for Gemini setup.
	NeedsGeminiInit bool

	// ClaudePlugins are plugins baked into the image.
	// Format: "plugin-name@marketplace-name"
	ClaudePlugins []string

	// Hooks contains user-defined lifecycle hook commands.
	// Different hooks produce different image tags.
	Hooks *HooksConfig
}

// ImageTag generates a deterministic image tag for a set of dependencies.
func ImageTag(deps []Dependency, opts *ImageTagOptions) string {
	if opts == nil {
		opts = &ImageTagOptions{}
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
	if opts.NeedsSSH {
		hashInput += ",ssh:agent"
	}
	if opts.NeedsClaudeInit {
		hashInput += ",claude:init"
	}
	if opts.NeedsCodexInit {
		hashInput += ",codex:init"
	}
	if opts.NeedsGeminiInit {
		hashInput += ",gemini:init"
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
