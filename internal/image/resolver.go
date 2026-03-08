// Package image handles container image selection.
package image

import "github.com/majorcontext/moat/internal/deps"

// DefaultImage is the default container image.
const DefaultImage = "ubuntu:22.04"

// Resolve determines the image to use based on dependencies and options.
// If depList is provided and non-empty, or if options require custom setup,
// returns the tag for a built image. Otherwise returns the default base image.
func Resolve(depList []deps.Dependency, spec *deps.ImageSpec) string {
	if spec == nil {
		spec = &deps.ImageSpec{}
	}
	if !spec.NeedsCustomImage(len(depList) > 0) {
		return DefaultImage
	}
	return deps.ImageTag(depList, spec)
}
