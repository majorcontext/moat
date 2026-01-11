// Package image handles container image selection.
package image

import "github.com/andybons/agentops/internal/config"

// DefaultImage is the default container image.
const DefaultImage = "ubuntu:22.04"

// Resolve selects the best base image for the given config.
func Resolve(cfg *config.Config) string {
	if cfg == nil {
		return DefaultImage
	}

	// Count runtimes specified
	runtimes := 0
	if cfg.Runtime.Node != "" {
		runtimes++
	}
	if cfg.Runtime.Python != "" {
		runtimes++
	}
	if cfg.Runtime.Go != "" {
		runtimes++
	}

	// If multiple runtimes, use ubuntu base
	if runtimes > 1 {
		return DefaultImage
	}

	// Single runtime - use official image
	if cfg.Runtime.Node != "" {
		return "node:" + cfg.Runtime.Node
	}
	if cfg.Runtime.Python != "" {
		return "python:" + cfg.Runtime.Python
	}
	if cfg.Runtime.Go != "" {
		return "golang:" + cfg.Runtime.Go
	}

	return DefaultImage
}
