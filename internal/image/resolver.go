// Package image handles container image selection.
package image

import "github.com/majorcontext/moat/internal/deps"

// DefaultImage is the default container image.
const DefaultImage = "ubuntu:22.04"

// ResolveOptions configures image resolution.
type ResolveOptions struct {
	// NeedsSSH indicates the image needs SSH packages and init script.
	NeedsSSH bool

	// NeedsClaudeInit indicates the image needs the init script for Claude setup.
	NeedsClaudeInit bool

	// NeedsCodexInit indicates the image needs the init script for Codex setup.
	NeedsCodexInit bool

	// ClaudePlugins are plugins baked into the image.
	// Format: "plugin-name@marketplace-name"
	ClaudePlugins []string
}

// Resolve determines the image to use based on dependencies and options.
// If depList is provided and non-empty, or if options require custom setup,
// returns the tag for a built image. Otherwise returns the default base image.
func Resolve(depList []deps.Dependency, opts *ResolveOptions) string {
	if opts == nil {
		opts = &ResolveOptions{}
	}

	// Need custom image if we have dependencies, SSH, Claude init, Codex init, or plugins
	needsCustomImage := len(depList) > 0 || opts.NeedsSSH || opts.NeedsClaudeInit || opts.NeedsCodexInit || len(opts.ClaudePlugins) > 0
	if !needsCustomImage {
		return DefaultImage
	}

	return deps.ImageTag(depList, &deps.ImageTagOptions{
		NeedsSSH:        opts.NeedsSSH,
		NeedsClaudeInit: opts.NeedsClaudeInit,
		NeedsCodexInit:  opts.NeedsCodexInit,
		ClaudePlugins:   opts.ClaudePlugins,
	})
}
