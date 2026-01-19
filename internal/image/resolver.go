// Package image handles container image selection.
package image

import "github.com/andybons/moat/internal/deps"

// DefaultImage is the default container image.
const DefaultImage = "ubuntu:22.04"

// Resolve determines the image to use based on dependencies.
// If depList is provided and non-empty, returns the tag for a built image.
// Otherwise returns the default base image.
func Resolve(depList []deps.Dependency) string {
	if len(depList) == 0 {
		return DefaultImage
	}
	return deps.ImageTag(depList)
}
