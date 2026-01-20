// Package image handles container image selection.
package image

import "github.com/andybons/moat/internal/deps"

// DefaultImage is the default container image.
const DefaultImage = "ubuntu:22.04"

// ResolveOptions configures image resolution.
type ResolveOptions struct {
	// NeedsSSH indicates the image needs SSH packages and init script.
	NeedsSSH bool
}

// Resolve determines the image to use based on dependencies and options.
// If depList is provided and non-empty, or if options require custom setup,
// returns the tag for a built image. Otherwise returns the default base image.
func Resolve(depList []deps.Dependency, opts *ResolveOptions) string {
	if opts == nil {
		opts = &ResolveOptions{}
	}

	// Need custom image if we have dependencies or SSH
	needsCustomImage := len(depList) > 0 || opts.NeedsSSH
	if !needsCustomImage {
		return DefaultImage
	}

	return deps.ImageTag(depList, &deps.ImageTagOptions{
		NeedsSSH: opts.NeedsSSH,
	})
}
