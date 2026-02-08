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

	// NeedsGeminiInit indicates the image needs the init script for Gemini setup.
	NeedsGeminiInit bool

	// ClaudePlugins are plugins baked into the image.
	// Format: "plugin-name@marketplace-name"
	ClaudePlugins []string

	// Hooks contains user-defined lifecycle hook commands.
	Hooks *deps.HooksConfig
}

// Resolve determines the image to use based on dependencies and options.
// If depList is provided and non-empty, or if options require custom setup,
// returns the tag for a built image. Otherwise returns the default base image.
func Resolve(depList []deps.Dependency, opts *ResolveOptions) string {
	if opts == nil {
		opts = &ResolveOptions{}
	}

	hasHooks := opts.Hooks != nil && (opts.Hooks.PostBuild != "" || opts.Hooks.PostBuildRoot != "" || opts.Hooks.PreRun != "")

	// Need custom image if we have dependencies, SSH, Claude init, Codex init, Gemini init, plugins, or hooks
	needsCustomImage := len(depList) > 0 || opts.NeedsSSH || opts.NeedsClaudeInit || opts.NeedsCodexInit || opts.NeedsGeminiInit || len(opts.ClaudePlugins) > 0 || hasHooks
	if !needsCustomImage {
		return DefaultImage
	}

	return deps.ImageTag(depList, &deps.ImageTagOptions{
		NeedsSSH:        opts.NeedsSSH,
		NeedsClaudeInit: opts.NeedsClaudeInit,
		NeedsCodexInit:  opts.NeedsCodexInit,
		NeedsGeminiInit: opts.NeedsGeminiInit,
		ClaudePlugins:   opts.ClaudePlugins,
		Hooks:           opts.Hooks,
	})
}
